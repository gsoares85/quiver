package httpengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoares85/quiver/internal/model"
	"github.com/stretchr/testify/require"
)

// fakeOpener serves file contents from memory so body tests never touch the disk. It
// counts opens per path so a redirect replay can be told apart from a rewound reader, and
// counts closes so a test can prove a streaming goroutine released its file.
type fakeOpener struct {
	files    map[string]string
	err      error
	closeErr error
	closes   atomic.Int32

	// mu guards opens: a multipart body is streamed from its own goroutine, so opens are
	// counted from there while a test reads them.
	mu    sync.Mutex
	opens map[string]int
}

func newFakeOpener(files map[string]string) *fakeOpener {
	return &fakeOpener{files: files, opens: map[string]int{}}
}

func (f *fakeOpener) Open(path string) (io.ReadCloser, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	content, ok := f.files[path]
	if !ok {
		return nil, 0, fmt.Errorf("open %s: %w", path, os.ErrNotExist)
	}
	f.mu.Lock()
	f.opens[path]++
	f.mu.Unlock()
	return &fakeFile{Reader: strings.NewReader(content), opener: f}, int64(len(content)), nil
}

// openCount reports how many times path has been opened.
func (f *fakeOpener) openCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens[path]
}

// fakeFile is one open file from a fakeOpener.
type fakeFile struct {
	io.Reader
	opener *fakeOpener
}

func (f *fakeFile) Close() error {
	f.opener.closes.Add(1)
	return f.opener.closeErr
}

// readBody opens a wire body and returns everything it produces.
func readBody(t *testing.T, b wireBody) string {
	t.Helper()
	require.NotNil(t, b.open)
	rc, err := b.open()
	require.NoError(t, err)
	defer func() { require.NoError(t, rc.Close()) }()
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(data)
}

func TestCheckMethod(t *testing.T) {
	tests := []struct {
		method string
		want   Kind // empty means accepted
	}{
		{method: "GET"},
		{method: "POST"},
		{method: "PATCH"},
		{method: "HEAD"},
		{method: "OPTIONS"},
		{method: "TRACE"},
		{method: "CONNECT", want: KindUnsupported}, // authority-form; http.Client cannot send it
		{method: "WS", want: KindUnsupported},
		{method: "GRPC", want: KindUnsupported},
		{method: "GRAPHQL", want: KindUnsupported},
		{method: "FETCH", want: KindRequest},
		{method: "get", want: KindRequest}, // methods are stored uppercase; no normalizing
		{method: "", want: KindRequest},
	}

	for _, tt := range tests {
		t.Run("method "+tt.method, func(t *testing.T) {
			err := checkMethod(tt.method, "https://x/v1")
			if tt.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Equal(t, tt.want, KindOf(err))
			require.Equal(t, opBuildRequest, err.(*Error).Op)
		})
	}
}

func TestParseURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Kind // empty means accepted
	}{
		{name: "https", raw: "https://api.example.com/v1/users?page=2"},
		{name: "http with port", raw: "http://127.0.0.1:8080/health"},
		{name: "relative", raw: "v1/users", want: KindRequest},
		{name: "scheme relative", raw: "//api.example.com/v1", want: KindRequest},
		{name: "no host", raw: "https:///v1/users", want: KindRequest},
		{name: "unparseable", raw: "http://[::1", want: KindRequest},
		{name: "unresolved placeholder", raw: "{{baseUrl}}/users", want: KindRequest},
		{name: "other scheme", raw: "ftp://files.example.com/x", want: KindUnsupported},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := parseURL(tt.raw)
			if tt.want == "" {
				require.NoError(t, err)
				require.Equal(t, tt.raw, u.String())
				return
			}
			require.Error(t, err)
			require.Equal(t, tt.want, KindOf(err))
			require.Equal(t, tt.raw, err.(*Error).URL)
		})
	}
}

func TestAppendQueryPreservesAuthoredOrder(t *testing.T) {
	u, err := parseURL("https://api.example.com/search?q=go")
	require.NoError(t, err)

	appendQuery(u, []model.Param{
		{Key: "zebra", Value: "1"},
		{Key: "alpha", Value: "2"},
		{Key: "filter", Value: "a b&c=d"},
		{Key: "", Value: "dropped"}, // a blank row carries nothing to the wire
		{Key: "tag", Value: "ação"},
	})

	require.Equal(t, "q=go&zebra=1&alpha=2&filter=a+b%26c%3Dd&tag=a%C3%A7%C3%A3o", u.RawQuery)
	require.Equal(t,
		"https://api.example.com/search?q=go&zebra=1&alpha=2&filter=a+b%26c%3Dd&tag=a%C3%A7%C3%A3o",
		u.String())
}

func TestAppendQueryWithoutExistingQuery(t *testing.T) {
	u, err := parseURL("https://api.example.com/search")
	require.NoError(t, err)
	appendQuery(u, []model.Param{{Key: "q", Value: "go"}})
	require.Equal(t, "q=go", u.RawQuery)

	// No parameters must leave the URL untouched.
	u2, err := parseURL("https://api.example.com/search?q=go")
	require.NoError(t, err)
	appendQuery(u2, nil)
	require.Equal(t, "q=go", u2.RawQuery)
}

func TestApplyHeaders(t *testing.T) {
	hr, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://x/v1", nil)
	require.NoError(t, err)

	require.NoError(t, applyHeaders(hr, []model.Header{
		{Key: "accept", Value: "application/json"},
		{Key: "Accept", Value: "text/plain"}, // a repeated key accumulates
		{Key: "X-Trace-Id", Value: "abc"},
		{Key: "host", Value: "internal.example.com"},
		{Key: "", Value: "dropped"},
	}, "application/json"))

	require.Equal(t, []string{"application/json", "text/plain"}, hr.Header.Values("Accept"))
	require.Equal(t, "abc", hr.Header.Get("X-Trace-Id"))
	require.Equal(t, "application/json", hr.Header.Get("Content-Type"))
	require.Equal(t, "internal.example.com", hr.Host, "a Host header is lifted onto Request.Host")
	require.Empty(t, hr.Header.Get("Host"), "the transport ignores Header[Host]")
	require.NotContains(t, hr.Header, "")
}

func TestApplyHeadersExplicitContentTypeWins(t *testing.T) {
	hr, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://x/v1", nil)
	require.NoError(t, err)

	require.NoError(t, applyHeaders(hr, []model.Header{
		{Key: "Content-Type", Value: "application/vnd.api+json"},
	}, "application/json"))

	require.Equal(t, []string{"application/vnd.api+json"}, hr.Header.Values("Content-Type"),
		"the inferred type is only a default and must be replaced, not appended to")
}

func TestApplyHeadersRejectsWhatTheTransportCannotSend(t *testing.T) {
	// The transport refuses malformed names and values at write time with a message
	// classify cannot attribute, so the failure would reach the user as "unknown" instead
	// of "invalid request". These become plausible once variables land in header fields.
	tests := []struct {
		name    string
		header  model.Header
		wantMsg string
	}{
		{
			name:    "space in the name",
			header:  model.Header{Key: "X Api Key", Value: "k-1"},
			wantMsg: `header name "X Api Key" is not a valid token`,
		},
		{
			name:    "separator in the name",
			header:  model.Header{Key: "X-Api:Key", Value: "k-1"},
			wantMsg: `header name "X-Api:Key" is not a valid token`,
		},
		{
			name:    "newline in the name",
			header:  model.Header{Key: "X-Api\nKey", Value: "k-1"},
			wantMsg: "is not a valid token",
		},
		{
			name:    "injected value",
			header:  model.Header{Key: "X-Trace-Id", Value: "abc\r\nX-Evil: 1"},
			wantMsg: `header "X-Trace-Id" value contains a line break`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hr, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/v1", nil)
			require.NoError(t, err)
			require.ErrorContains(t, applyHeaders(hr, []model.Header{tt.header}, ""), tt.wantMsg)
		})
	}
}

func TestApplyHeadersAcceptsUnusualButValidNames(t *testing.T) {
	hr, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/v1", nil)
	require.NoError(t, err)

	require.NoError(t, applyHeaders(hr, []model.Header{
		{Key: "X-Api-Key_v2", Value: "k-1"},
		{Key: "If-None-Match", Value: `W/"etag"`},
		{Key: "$weird*but'legal", Value: "1"},
	}, ""))

	require.Equal(t, "k-1", hr.Header.Get("X-Api-Key_v2"))
	require.Equal(t, `W/"etag"`, hr.Header.Get("If-None-Match"))
}

func TestBuildBodyTextShapes(t *testing.T) {
	tests := []struct {
		name        string
		body        *model.Body
		wantPayload string
		wantType    string
	}{
		{name: "nil body", body: nil},
		{name: "none", body: &model.Body{Type: model.BodyNone}},
		{
			name:        "json",
			body:        &model.Body{Type: model.BodyJSON, Text: `{"name":"ada"}`},
			wantPayload: `{"name":"ada"}`,
			wantType:    "application/json",
		},
		{
			name:        "text",
			body:        &model.Body{Type: model.BodyText, Text: "hello"},
			wantPayload: "hello",
			wantType:    "text/plain; charset=utf-8",
		},
		{
			name:        "xml",
			body:        &model.Body{Type: model.BodyXML, Text: "<user/>"},
			wantPayload: "<user/>",
			wantType:    "application/xml",
		},
		{
			name: "form keeps authored order",
			body: &model.Body{Type: model.BodyForm, Form: []model.Param{
				{Key: "zebra", Value: "1"},
				{Key: "alpha", Value: "hello world"},
				{Key: "", Value: "dropped"},
			}},
			wantPayload: "zebra=1&alpha=hello+world",
			wantType:    "application/x-www-form-urlencoded",
		},
		{
			name: "graphql envelope",
			body: &model.Body{
				Type: model.BodyGraphQL,
				Text: "query User($id: ID!) { user(id: $id) { name } }",
				GraphQL: &model.GraphQL{
					Variables:     `{"id": "42"}`,
					OperationName: "User",
				},
			},
			wantPayload: `{"query":"query User($id: ID!) { user(id: $id) { name } }","variables":{"id":"42"},"operationName":"User"}`,
			wantType:    "application/json",
		},
		{
			name:        "graphql without variables",
			body:        &model.Body{Type: model.BodyGraphQL, Text: "{ me { id } }"},
			wantPayload: `{"query":"{ me { id } }"}`,
			wantType:    "application/json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := buildBody(tt.body, newFakeOpener(nil))
			require.NoError(t, err)

			if tt.wantType == "" {
				require.Nil(t, body.open, "a bodyless request must send nothing")
				require.Zero(t, body.contentLength)
				require.Empty(t, body.contentType)
				return
			}
			require.Equal(t, tt.wantType, body.contentType)
			require.Equal(t, int64(len(tt.wantPayload)), body.contentLength)
			require.Equal(t, tt.wantPayload, readBody(t, body))
			require.Equal(t, tt.wantPayload, readBody(t, body), "re-opening must replay the payload")
		})
	}
}

func TestBuildBodyErrors(t *testing.T) {
	opener := newFakeOpener(map[string]string{"/tmp/logo.png": "PNG"})

	tests := []struct {
		name    string
		body    *model.Body
		wantMsg string
	}{
		{
			name:    "invalid graphql variables",
			body:    &model.Body{Type: model.BodyGraphQL, Text: "{ me }", GraphQL: &model.GraphQL{Variables: "not json"}},
			wantMsg: "encode graphql body",
		},
		{
			name:    "binary without a file",
			body:    &model.Body{Type: model.BodyBinary},
			wantMsg: "binary body needs exactly one file, got 0",
		},
		{
			name: "binary with two files",
			body: &model.Body{Type: model.BodyBinary, Files: []model.FileRef{
				{Path: "/tmp/logo.png"}, {Path: "/tmp/logo.png"},
			}},
			wantMsg: "binary body needs exactly one file, got 2",
		},
		{
			name:    "binary file missing",
			body:    &model.Body{Type: model.BodyBinary, Files: []model.FileRef{{Path: "/tmp/nope.png"}}},
			wantMsg: "/tmp/nope.png",
		},
		{
			name:    "multipart file without a field name",
			body:    &model.Body{Type: model.BodyMultipart, Files: []model.FileRef{{Path: "/tmp/logo.png"}}},
			wantMsg: "multipart file 0 (/tmp/logo.png) has no field name",
		},
		{
			name: "multipart file missing",
			body: &model.Body{Type: model.BodyMultipart, Files: []model.FileRef{
				{Field: "avatar", Path: "/tmp/nope.png"},
			}},
			wantMsg: "/tmp/nope.png",
		},
		{
			name:    "unknown type",
			body:    &model.Body{Type: model.BodyType("yaml")},
			wantMsg: `unknown body type "yaml"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildBody(tt.body, opener)
			require.ErrorContains(t, err, tt.wantMsg)
		})
	}
}

func TestBuildBodyBinaryStreamsTheFile(t *testing.T) {
	opener := newFakeOpener(map[string]string{"/tmp/report.pdf": "%PDF-1.7 payload"})

	body, err := buildBody(&model.Body{
		Type:  model.BodyBinary,
		Files: []model.FileRef{{Path: "/tmp/report.pdf"}},
	}, opener)
	require.NoError(t, err)

	require.Equal(t, "application/pdf", body.contentType)
	require.Equal(t, int64(len("%PDF-1.7 payload")), body.contentLength,
		"an exact length matters: many APIs reject a chunked upload")
	require.Equal(t, "%PDF-1.7 payload", readBody(t, body))
	require.Equal(t, "%PDF-1.7 payload", readBody(t, body), "a redirect replay re-reads the file")

	// One open to sample the size, then one per send — never a reader that is already
	// drained.
	require.Equal(t, 3, opener.openCount("/tmp/report.pdf"))
}

func TestBuildBodyMultipart(t *testing.T) {
	opener := newFakeOpener(map[string]string{
		"/tmp/logo.png":  "PNGDATA",
		"/tmp/notes.bin": "RAW",
	})

	body, err := buildBody(&model.Body{
		Type: model.BodyMultipart,
		Form: []model.Param{
			{Key: "name", Value: "ada"},
			{Key: "", Value: "dropped"},
		},
		Files: []model.FileRef{
			{Field: "avatar", Path: "/tmp/logo.png"},
			{Field: `we"ird`, Path: "/tmp/notes.bin"},
		},
	}, opener)
	require.NoError(t, err)
	require.Equal(t, int64(-1), body.contentLength, "a streamed payload has no known length")

	mediaType, params, err := mime.ParseMediaType(body.contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	require.NotEmpty(t, params["boundary"])

	// Assert by parsing rather than against golden bytes: the boundary is random.
	parse := func() []part {
		rc, err := body.open()
		require.NoError(t, err)
		defer func() { require.NoError(t, rc.Close()) }()

		var got []part
		mr := multipart.NewReader(rc, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			data, err := io.ReadAll(p)
			require.NoError(t, err)
			got = append(got, part{
				name:        p.FormName(),
				fileName:    p.FileName(),
				contentType: p.Header.Get("Content-Type"),
				content:     string(data),
			})
		}
		return got
	}

	want := []part{
		{name: "name", content: "ada"},
		{name: "avatar", fileName: "logo.png", contentType: "image/png", content: "PNGDATA"},
		{name: `we"ird`, fileName: "notes.bin", contentType: "application/octet-stream", content: "RAW"},
	}
	require.Equal(t, want, parse(), "fields come first, then files, in authored order")
	require.Equal(t, want, parse(), "a redirect replay rebuilds the identical payload")

	// The boundary is fixed once, so the Content-Type stays valid for every replay.
	mediaType2, params2, err := mime.ParseMediaType(body.contentType)
	require.NoError(t, err)
	require.Equal(t, mediaType, mediaType2)
	require.Equal(t, params["boundary"], params2["boundary"])
}

// part is one decoded multipart section, compared as a whole in the test above.
type part struct {
	name        string
	fileName    string
	contentType string
	content     string
}

func TestBuildBodyMultipartSurfacesReadFailures(t *testing.T) {
	// A file that disappears between building the request and sending it can only fail
	// mid-stream; the reader must see the error rather than a truncated payload.
	opener := newFakeOpener(map[string]string{"/tmp/logo.png": "PNGDATA"})
	body, err := buildBody(&model.Body{
		Type:  model.BodyMultipart,
		Files: []model.FileRef{{Field: "avatar", Path: "/tmp/logo.png"}},
	}, opener)
	require.NoError(t, err)

	opener.err = os.ErrPermission
	rc, err := body.open()
	require.NoError(t, err)
	defer func() { require.NoError(t, rc.Close()) }()
	_, err = io.ReadAll(rc)
	require.ErrorIs(t, err, os.ErrPermission)
}

func TestBuildBodyReportsCloseFailure(t *testing.T) {
	// Sampling a file's size must not swallow a close failure: a file that cannot be
	// closed is not a file we can promise to stream twice.
	opener := newFakeOpener(map[string]string{"/tmp/report.pdf": "payload"})
	opener.closeErr = errors.New("disk went away")

	_, err := buildBody(&model.Body{
		Type:  model.BodyBinary,
		Files: []model.FileRef{{Path: "/tmp/report.pdf"}},
	}, opener)
	require.ErrorContains(t, err, "close /tmp/report.pdf: disk went away")
}

func TestBuildBodyMultipartUnwindsWhenTheReaderGoesAway(t *testing.T) {
	// The transport abandons a request body whenever a send fails part-way. The writer
	// goroutine must then unwind and release the file it was streaming instead of
	// blocking on the pipe forever.
	opener := newFakeOpener(map[string]string{"/tmp/logo.png": strings.Repeat("PNG", 4096)})
	body, err := buildBody(&model.Body{
		Type:  model.BodyMultipart,
		Files: []model.FileRef{{Field: "avatar", Path: "/tmp/logo.png"}},
	}, opener)
	require.NoError(t, err)

	rc, err := body.open()
	require.NoError(t, err)
	_, err = io.CopyN(io.Discard, rc, 16) // let the goroutine get as far as the file
	require.NoError(t, err)
	require.NoError(t, rc.Close())

	require.Eventually(t, func() bool { return opener.closes.Load() > 1 },
		time.Second, time.Millisecond, "the streaming goroutine never released the file")
	_, err = io.ReadAll(rc)
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestFileContentType(t *testing.T) {
	require.Equal(t, "image/png", fileContentType("/tmp/logo.png"))
	require.Equal(t, "application/octet-stream", fileContentType("/tmp/blob"))
	require.Equal(t, "application/octet-stream", fileContentType("/tmp/archive.quiverz"))
}

func TestNewRequest(t *testing.T) {
	opener := newFakeOpener(nil)
	req := model.Request{
		Method: http.MethodPost,
		URL:    "https://api.example.com/v1/users?source=cli",
		Query:  []model.Param{{Key: "notify", Value: "true"}},
		Headers: []model.Header{
			{Key: "X-Trace-Id", Value: "abc"},
			{Key: "Content-Type", Value: "application/vnd.api+json"},
		},
		Body: &model.Body{Type: model.BodyJSON, Text: `{"name":"ada"}`},
	}

	ctx := context.Background()
	hr, err := newRequest(ctx, req, opener)
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, hr.Method)
	require.Equal(t, "https://api.example.com/v1/users?source=cli&notify=true", hr.URL.String())
	require.Equal(t, "abc", hr.Header.Get("X-Trace-Id"))
	require.Equal(t, "application/vnd.api+json", hr.Header.Get("Content-Type"))
	require.Equal(t, int64(len(`{"name":"ada"}`)), hr.ContentLength)
	require.Equal(t, ctx, hr.Context())

	data, err := io.ReadAll(hr.Body)
	require.NoError(t, err)
	require.Equal(t, `{"name":"ada"}`, string(data))

	// GetBody is what lets the client resend the payload after a redirect.
	require.NotNil(t, hr.GetBody)
	replay, err := hr.GetBody()
	require.NoError(t, err)
	data, err = io.ReadAll(replay)
	require.NoError(t, err)
	require.Equal(t, `{"name":"ada"}`, string(data))
}

func TestNewRequestWithoutBody(t *testing.T) {
	hr, err := newRequest(context.Background(), model.Request{
		Method: http.MethodGet,
		URL:    "https://api.example.com/v1/users",
	}, newFakeOpener(nil))
	require.NoError(t, err)

	require.Nil(t, hr.Body)
	require.Nil(t, hr.GetBody)
	require.Zero(t, hr.ContentLength)
	require.Empty(t, hr.Header.Get("Content-Type"), "a bodyless request declares no content type")
}

func TestNewRequestStreamsUnknownLength(t *testing.T) {
	opener := newFakeOpener(map[string]string{"/tmp/logo.png": "PNGDATA"})
	hr, err := newRequest(context.Background(), model.Request{
		Method: http.MethodPost,
		URL:    "https://api.example.com/v1/avatars",
		Body: &model.Body{
			Type:  model.BodyMultipart,
			Files: []model.FileRef{{Field: "avatar", Path: "/tmp/logo.png"}},
		},
	}, opener)
	require.NoError(t, err)

	require.Equal(t, int64(-1), hr.ContentLength, "-1 tells the transport to chunk the upload")
	require.Contains(t, hr.Header.Get("Content-Type"), "multipart/form-data; boundary=")
	require.NotNil(t, hr.GetBody)
}

func TestNewRequestErrors(t *testing.T) {
	tests := []struct {
		name string
		req  model.Request
		want Kind
	}{
		{
			name: "unknown method",
			req:  model.Request{Method: "FETCH", URL: "https://x/v1"},
			want: KindRequest,
		},
		{
			name: "pseudo method",
			req:  model.Request{Method: "WS", URL: "wss://x/socket"},
			want: KindUnsupported,
		},
		{
			name: "unresolved url",
			req:  model.Request{Method: http.MethodGet, URL: "{{baseUrl}}/users"},
			want: KindRequest,
		},
		{
			name: "malformed header name",
			req: model.Request{
				Method:  http.MethodGet,
				URL:     "https://x/v1",
				Headers: []model.Header{{Key: "X Api Key", Value: "k-1"}},
			},
			want: KindRequest,
		},
		{
			name: "unreadable body file",
			req: model.Request{
				Method: http.MethodPost,
				URL:    "https://x/v1",
				Body:   &model.Body{Type: model.BodyBinary, Files: []model.FileRef{{Path: "/tmp/nope.png"}}},
			},
			want: KindRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hr, err := newRequest(context.Background(), tt.req, newFakeOpener(nil))
			require.Nil(t, hr)
			require.Error(t, err)
			require.Equal(t, tt.want, KindOf(err))
			require.Equal(t, opBuildRequest, err.(*Error).Op)
			require.Equal(t, tt.req.URL, err.(*Error).URL)
		})
	}
}

func TestNewRequestLeavesNoFileOpenWhenItIsRejected(t *testing.T) {
	// Nothing closes a body that never reaches a request, so the payload must not be
	// opened while a later step can still reject the request. Sampling the file's size is
	// the only open allowed here, and it closes itself.
	opener := newFakeOpener(map[string]string{"/tmp/report.pdf": "payload"})

	hr, err := newRequest(context.Background(), model.Request{
		Method: http.MethodPost,
		URL:    "https://api.example.com/v1",
		Body:   &model.Body{Type: model.BodyBinary, Files: []model.FileRef{{Path: "/tmp/report.pdf"}}},
		Auth:   &model.Auth{Type: model.AuthInherit}, // rejected after the payload would have opened
	}, opener)
	require.Nil(t, hr)
	require.Equal(t, KindRequest, KindOf(err))

	require.Equal(t, 1, opener.openCount("/tmp/report.pdf"), "a doomed request must not open its payload")
	require.Equal(t, int32(1), opener.closes.Load(), "every open is matched by a close")
}

func TestNewRequestDoesNotStrandTheMultipartWriter(t *testing.T) {
	// A multipart payload is written by a goroutine feeding a pipe. Opening it before the
	// request is known to be sendable would strand that goroutine forever — blocked on a
	// pipe nobody will read, holding the file it opened. An unresolved token on a request
	// with an attachment is the everyday way to hit this.
	opener := newFakeOpener(map[string]string{"/tmp/logo.png": strings.Repeat("PNG", 8192)})

	hr, err := newRequest(context.Background(), model.Request{
		Method: http.MethodPost,
		URL:    "https://api.example.com/v1",
		Body: &model.Body{
			Type:  model.BodyMultipart,
			Files: []model.FileRef{{Field: "avatar", Path: "/tmp/logo.png"}},
		},
		Auth: &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: ""}},
	}, opener)
	require.Nil(t, hr)
	require.Equal(t, KindRequest, KindOf(err))

	// A stranded writer always gives itself away: it opens the file it is streaming and
	// blocks before its deferred Close can run. Never rather than a single check, because
	// the goroutine would be spawned during newRequest and reach the file a moment later.
	require.Never(t, func() bool { return opener.openCount("/tmp/logo.png") > 1 },
		200*time.Millisecond, 10*time.Millisecond,
		"the payload was opened by a writer goroutine that nothing will ever read or close")
	require.Equal(t, int32(1), opener.closes.Load(), "every open is matched by a close")
}

func TestNewRequestReportsBodyOpenFailure(t *testing.T) {
	// The pre-flight check passes, then the first open fails: the request must not be
	// built with a half-usable body.
	opener := newFakeOpener(map[string]string{"/tmp/logo.png": "PNGDATA"})
	req := model.Request{
		Method: http.MethodPost,
		URL:    "https://x/v1",
		Body: &model.Body{
			Type:  model.BodyBinary,
			Files: []model.FileRef{{Path: "/tmp/logo.png"}},
		},
	}

	openCalls := 0
	failing := &recordingOpener{inner: opener, failAfter: 1, calls: &openCalls}
	hr, err := newRequest(context.Background(), req, failing)
	require.Nil(t, hr)
	require.Error(t, err)
	require.Equal(t, KindRequest, KindOf(err))
	require.ErrorIs(t, err, os.ErrPermission)
}

// recordingOpener delegates to inner until failAfter opens have happened, then fails.
type recordingOpener struct {
	inner     FileOpener
	failAfter int
	calls     *int
}

func (r *recordingOpener) Open(path string) (io.ReadCloser, int64, error) {
	*r.calls++
	if *r.calls > r.failAfter {
		return nil, 0, os.ErrPermission
	}
	return r.inner.Open(path)
}

func TestOSFileOpener(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"ok":true}`), 0o644))

	var opener FileOpener = osFileOpener{}
	rc, size, err := opener.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Close() })

	require.Equal(t, int64(len(`{"ok":true}`)), size)
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, `{"ok":true}`, string(data))

	_, _, err = opener.Open(filepath.Join(dir, "missing.json"))
	require.ErrorIs(t, err, os.ErrNotExist)

	// A directory opens but cannot be sized as a payload on every platform; reading it
	// must fail rather than silently send nothing.
	rc, _, err = opener.Open(dir)
	if err == nil {
		t.Cleanup(func() { _ = rc.Close() })
		_, err = io.ReadAll(rc)
	}
	require.Error(t, err)
}
