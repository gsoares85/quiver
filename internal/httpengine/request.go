package httpengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gsoares85/quiver/internal/model"
)

// opBuildRequest labels failures raised while turning a model.Request into a wire
// request, before anything is sent.
const opBuildRequest = "build request"

// httpMethods are the methods this engine sends over HTTP. model.ValidMethod also
// accepts the WS, GRPC, and GRAPHQL pseudo-methods, which dedicated protocol handlers
// own, so they are rejected here rather than silently sent as HTTP verbs.
var httpMethods = map[string]struct{}{
	"GET":     {},
	"POST":    {},
	"PUT":     {},
	"PATCH":   {},
	"DELETE":  {},
	"HEAD":    {},
	"OPTIONS": {},
	"TRACE":   {},
	"CONNECT": {},
}

// FileOpener opens the files a multipart or binary body references. It is an interface
// so the engine's only filesystem authority is explicit and injectable: the default
// implementation is the one place that touches real files, and tests exercise uploads
// without them.
type FileOpener interface {
	// Open returns the file's contents and its size in bytes. A negative size means the
	// size is unknown, which makes the upload chunked.
	Open(path string) (io.ReadCloser, int64, error)
}

// osFileOpener reads referenced files from the real filesystem.
type osFileOpener struct{}

func (osFileOpener) Open(path string) (io.ReadCloser, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("stat %s: %w", path, err)
	}
	return f, info.Size(), nil
}

// wireBody is a payload ready to send: a way to open the bytes — repeatedly, so a
// followed redirect can replay them — the Content-Type the body implies, and its length
// (-1 when it can only be discovered by sending it).
type wireBody struct {
	open          func() (io.ReadCloser, error) // nil when the request carries no body
	contentType   string
	contentLength int64
}

// quoteEscaper mirrors mime/multipart's own escaping of quotes and backslashes inside
// Content-Disposition parameters, which the standard library keeps unexported.
var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

// newRequest builds the wire request for a resolved model.Request: it validates the
// method and URL, merges the authored query parameters, attaches a re-openable body,
// and applies the request's headers. Variables must already be substituted and auth
// inheritance flattened by the caller; errors are *Error values ready to surface.
func newRequest(ctx context.Context, req model.Request, opener FileOpener) (*http.Request, error) {
	if err := checkMethod(req.Method, req.URL); err != nil {
		return nil, err
	}
	u, err := parseURL(req.URL)
	if err != nil {
		return nil, err
	}
	appendQuery(u, req.Query)

	body, err := buildBody(req.Body, opener)
	if err != nil {
		return nil, newError(KindRequest, opBuildRequest, req.URL, err)
	}

	var initial io.ReadCloser
	if body.open != nil {
		if initial, err = body.open(); err != nil {
			return nil, newError(KindRequest, opBuildRequest, req.URL, err)
		}
	}

	hr, err := http.NewRequestWithContext(ctx, req.Method, u.String(), initial)
	if err != nil {
		return nil, newError(KindRequest, opBuildRequest, req.URL, err)
	}
	if body.open != nil {
		hr.ContentLength = body.contentLength
		hr.GetBody = body.open // lets the client replay the payload across a redirect
	}
	applyHeaders(hr, req.Headers, body.contentType)
	return hr, nil
}

// checkMethod rejects methods this engine does not send. It mirrors the model's
// case-sensitive convention (methods are stored uppercase) instead of normalizing, so a
// malformed request fails the same way everywhere.
func checkMethod(method, rawURL string) error {
	if _, ok := httpMethods[method]; ok {
		return nil
	}
	if model.ValidMethod(method) {
		return newError(KindUnsupported, opBuildRequest, rawURL,
			fmt.Errorf("method %s is not handled by the http engine", method))
	}
	return newError(KindRequest, opBuildRequest, rawURL, fmt.Errorf("unknown method %q", method))
}

// parseURL requires an absolute http(s) URL: the engine is handed a fully-resolved
// request, so a relative URL or a leftover placeholder is a caller bug, not something to
// guess a base for.
func parseURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, newError(KindRequest, opBuildRequest, rawURL, fmt.Errorf("parse url: %w", err))
	}
	switch {
	case u.Scheme == "":
		return nil, newError(KindRequest, opBuildRequest, rawURL,
			fmt.Errorf("url %q must be absolute, e.g. https://api.example.com", rawURL))
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, newError(KindUnsupported, opBuildRequest, rawURL,
			fmt.Errorf("unsupported url scheme %q", u.Scheme))
	case u.Host == "":
		return nil, newError(KindRequest, opBuildRequest, rawURL, fmt.Errorf("url %q has no host", rawURL))
	}
	return u, nil
}

// appendQuery adds the request's query parameters to whatever the URL already carries,
// preserving the order they were authored in. url.Values.Encode sorts keys, which would
// silently reorder a hand-written query, so the raw query is assembled here.
func appendQuery(u *url.URL, params []model.Param) {
	var b strings.Builder
	b.WriteString(u.RawQuery)
	for _, p := range params {
		if p.Key == "" {
			continue // a blank row carries no meaning on the wire
		}
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(p.Key))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(p.Value))
	}
	u.RawQuery = b.String()
}

// applyHeaders copies the request's headers onto the wire request. An inferred body
// Content-Type is only a default: an explicit header replaces it, while a key repeated
// by the user accumulates (two Accept entries stay two entries). A Host header is lifted
// onto Request.Host, because the transport ignores Header["Host"].
func applyHeaders(hr *http.Request, headers []model.Header, contentType string) {
	if contentType != "" {
		hr.Header.Set("Content-Type", contentType)
	}
	seen := make(map[string]bool, len(headers))
	for _, h := range headers {
		if h.Key == "" {
			continue
		}
		if strings.EqualFold(h.Key, "Host") {
			hr.Host = h.Value
			continue
		}
		key := http.CanonicalHeaderKey(h.Key)
		if seen[key] {
			hr.Header.Add(key, h.Value)
			continue
		}
		hr.Header.Set(key, h.Value)
		seen[key] = true
	}
}

// buildBody turns a request body into a re-openable payload. Text-shaped bodies are held
// in memory so they carry an exact Content-Length; multipart and binary bodies stream
// from disk so a large upload never sits in RAM.
func buildBody(b *model.Body, opener FileOpener) (wireBody, error) {
	if b == nil {
		return wireBody{}, nil
	}
	switch b.Type {
	case model.BodyNone:
		return wireBody{}, nil
	case model.BodyJSON:
		return memoryBody([]byte(b.Text), "application/json"), nil
	case model.BodyText:
		return memoryBody([]byte(b.Text), "text/plain; charset=utf-8"), nil
	case model.BodyXML:
		return memoryBody([]byte(b.Text), "application/xml"), nil
	case model.BodyGraphQL:
		return graphQLBody(b)
	case model.BodyForm:
		return memoryBody(encodeForm(b.Form), "application/x-www-form-urlencoded"), nil
	case model.BodyMultipart:
		return multipartBody(b, opener)
	case model.BodyBinary:
		return binaryBody(b, opener)
	default:
		return wireBody{}, fmt.Errorf("unknown body type %q", b.Type)
	}
}

// memoryBody wraps bytes the engine already holds. Re-opening is a fresh reader over the
// same slice, so a redirect replay is free and byte-identical.
func memoryBody(payload []byte, contentType string) wireBody {
	return wireBody{
		open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(payload)), nil
		},
		contentType:   contentType,
		contentLength: int64(len(payload)),
	}
}

// graphQLRequest is the GraphQL-over-HTTP envelope. Field order is fixed by the struct,
// so the encoded body is deterministic.
type graphQLRequest struct {
	Query         string          `json:"query"`
	Variables     json.RawMessage `json:"variables,omitempty"`
	OperationName string          `json:"operationName,omitempty"`
}

// graphQLBody wraps the query text and its variables in the standard JSON envelope. The
// GRAPHQL pseudo-method (schema introspection and its own request flow) is a separate
// protocol handler; this is just a POST whose body happens to be GraphQL.
func graphQLBody(b *model.Body) (wireBody, error) {
	env := graphQLRequest{Query: b.Text}
	if b.GraphQL != nil {
		env.OperationName = b.GraphQL.OperationName
		if vars := strings.TrimSpace(b.GraphQL.Variables); vars != "" {
			env.Variables = json.RawMessage(vars)
		}
	}
	payload, err := json.Marshal(env) // also validates the raw variables JSON
	if err != nil {
		return wireBody{}, fmt.Errorf("encode graphql body: %w", err)
	}
	return memoryBody(payload, "application/json"), nil
}

// encodeForm encodes fields as a urlencoded payload in authored order, for the same
// reason appendQuery does: url.Values.Encode would sort the keys.
func encodeForm(fields []model.Param) []byte {
	var b strings.Builder
	for _, f := range fields {
		if f.Key == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(f.Key))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(f.Value))
	}
	return []byte(b.String())
}

// multipartBody streams a multipart/form-data payload from an io.Pipe so file uploads
// are never buffered. The boundary is chosen once and reused by every open, keeping the
// Content-Type valid across a redirect replay. Referenced files are checked here so an
// unreadable path fails before anything is sent rather than mid-upload.
func multipartBody(b *model.Body, opener FileOpener) (wireBody, error) {
	for i, ref := range b.Files {
		if ref.Field == "" {
			return wireBody{}, fmt.Errorf("multipart file %d (%s) has no field name", i, ref.Path)
		}
		if _, err := statFile(opener, ref.Path); err != nil {
			return wireBody{}, err
		}
	}

	boundary := multipart.NewWriter(io.Discard).Boundary() // borrow the standard library's randomness
	fields, files := b.Form, b.Files
	open := func() (io.ReadCloser, error) {
		pr, pw := io.Pipe()
		go func() {
			mw := multipart.NewWriter(pw)
			if err := mw.SetBoundary(boundary); err != nil {
				_ = pw.CloseWithError(fmt.Errorf("set multipart boundary: %w", err))
				return
			}
			if err := writeMultipart(mw, fields, files, opener); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			_ = pw.CloseWithError(mw.Close()) // writes the closing boundary; nil means EOF
		}()
		return pr, nil
	}
	return wireBody{
		open:          open,
		contentType:   "multipart/form-data; boundary=" + boundary,
		contentLength: -1, // length is only known once the parts have been written
	}, nil
}

// writeMultipart writes the form fields followed by the file parts, in authored order.
func writeMultipart(mw *multipart.Writer, fields []model.Param, files []model.FileRef, opener FileOpener) error {
	for _, f := range fields {
		if f.Key == "" {
			continue
		}
		if err := mw.WriteField(f.Key, f.Value); err != nil {
			return fmt.Errorf("write field %s: %w", f.Key, err)
		}
	}
	for _, ref := range files {
		if err := writeFilePart(mw, ref, opener); err != nil {
			return err
		}
	}
	return nil
}

// writeFilePart streams one file into the multipart payload, labelling it with its
// guessed media type so servers that inspect part types behave as they would for a
// browser upload.
func writeFilePart(mw *multipart.Writer, ref model.FileRef, opener FileOpener) error {
	rc, _, err := opener.Open(ref.Path)
	if err != nil {
		return fmt.Errorf("open %s: %w", ref.Path, err)
	}
	defer func() { _ = rc.Close() }()

	header := make(textproto.MIMEHeader, 2)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
		quoteEscaper.Replace(ref.Field), quoteEscaper.Replace(filepath.Base(ref.Path))))
	header.Set("Content-Type", fileContentType(ref.Path))

	part, err := mw.CreatePart(header)
	if err != nil {
		return fmt.Errorf("create part %s: %w", ref.Field, err)
	}
	if _, err := io.Copy(part, rc); err != nil {
		return fmt.Errorf("copy %s: %w", ref.Path, err)
	}
	return nil
}

// binaryBody sends a single file as the whole payload.
func binaryBody(b *model.Body, opener FileOpener) (wireBody, error) {
	if len(b.Files) != 1 {
		return wireBody{}, fmt.Errorf("binary body needs exactly one file, got %d", len(b.Files))
	}
	path := b.Files[0].Path
	size, err := statFile(opener, path)
	if err != nil {
		return wireBody{}, err
	}
	return wireBody{
		open: func() (io.ReadCloser, error) {
			rc, _, err := opener.Open(path)
			if err != nil {
				return nil, fmt.Errorf("open %s: %w", path, err)
			}
			return rc, nil
		},
		contentType:   fileContentType(path),
		contentLength: size,
	}, nil
}

// statFile samples a referenced file's size and proves it is readable, so a bad path
// fails before anything goes on the wire. The file is re-opened when the body is
// actually sent: holding the handle here would leak it if the request is never sent, and
// a consumed reader could not be replayed after a redirect.
func statFile(opener FileOpener, path string) (int64, error) {
	rc, size, err := opener.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	if err := rc.Close(); err != nil {
		return 0, fmt.Errorf("close %s: %w", path, err)
	}
	return size, nil
}

// fileContentType guesses a file's media type from its extension, falling back to the
// generic binary type. An explicit Content-Type header always wins over it.
func fileContentType(path string) string {
	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
