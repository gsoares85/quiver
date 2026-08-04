package httpengine

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"
)

// Kind is a stable classification of a failed request. It is part of the engine's
// public contract: the desktop app turns a Kind into an actionable message and the CLI
// turns it into an exit code, so these strings are not free to change once released.
type Kind string

const (
	// KindRequest means the request could not be built at all — an unknown method, an
	// unusable URL, unflattened auth, or an unreadable body file. Nothing was sent.
	KindRequest Kind = "request"
	// KindUnsupported means the engine cannot speak what the request asks for, such as
	// the WS and GRPC pseudo-methods that other protocol handlers own.
	KindUnsupported Kind = "unsupported"
	// KindDNS means the host could not be resolved.
	KindDNS Kind = "dns"
	// KindConnRefused means the peer actively refused the connection — usually nothing
	// is listening on that port.
	KindConnRefused Kind = "connection_refused"
	// KindConnection covers the remaining transport failures: unreachable networks,
	// resets, and responses that end mid-stream.
	KindConnection Kind = "connection"
	// KindTLS means the TLS handshake or certificate verification failed.
	KindTLS Kind = "tls"
	// KindTimeout means the request exceeded its deadline.
	KindTimeout Kind = "timeout"
	// KindCanceled means the caller cancelled the request's context.
	KindCanceled Kind = "canceled"
	// KindTooManyRedirects means the redirect chain outgrew Settings.MaxRedirects.
	KindTooManyRedirects Kind = "too_many_redirects"
	// KindUnknown is the fallback for a failure the engine cannot attribute.
	KindUnknown Kind = "unknown"
)

// kindText is the wording rendered inside an *Error message. Keeping it beside the
// constants makes it obvious when a newly added kind forgets its phrasing.
var kindText = map[Kind]string{
	KindRequest:          "invalid request",
	KindUnsupported:      "unsupported request",
	KindDNS:              "dns lookup failed",
	KindConnRefused:      "connection refused",
	KindConnection:       "connection failed",
	KindTLS:              "tls error",
	KindTimeout:          "timed out",
	KindCanceled:         "canceled",
	KindTooManyRedirects: "too many redirects",
	KindUnknown:          "request failed",
}

// text returns k's wording, falling back to the raw kind so a kind added without an
// entry above still produces a usable message instead of an empty one.
func (k Kind) text() string {
	if t, ok := kindText[k]; ok {
		return t
	}
	if k == "" {
		return kindText[KindUnknown]
	}
	return string(k)
}

// ErrTooManyRedirects reports a redirect chain longer than the request allows. It is a
// sentinel because it has to travel out through http.Client's CheckRedirect hook, which
// passes an error and nothing else; every other engine failure is identified by Kind.
var ErrTooManyRedirects = errors.New("too many redirects")

// wsaeconnrefused is the Windows socket error for a refused connection. Windows reports
// WSAECONNREFUSED (10061) while syscall.ECONNREFUSED is an unrelated value there, so
// matching both keeps the classification identical across platforms without build tags.
const wsaeconnrefused = syscall.Errno(10061)

// Error is a request failure carrying a stable Kind. The cause is always wrapped, so a
// caller that wants the underlying detail can still reach it through errors.Is and
// errors.As while a front-end renders the Kind.
type Error struct {
	Kind Kind
	Op   string // the engine operation that failed, e.g. "send request"
	URL  string // the request URL, when known
	Err  error  // the wrapped cause; nil when the engine itself rejected the request
}

// Error renders "op url: kind: cause", omitting the parts that are not known.
func (e *Error) Error() string {
	var b strings.Builder
	switch {
	case e.Op != "" && e.URL != "":
		b.WriteString(e.Op + " " + e.URL + ": ")
	case e.Op != "":
		b.WriteString(e.Op + ": ")
	case e.URL != "":
		b.WriteString(e.URL + ": ")
	}
	b.WriteString(e.Kind.text())
	if e.Err != nil {
		b.WriteString(": " + e.Err.Error())
	}
	return b.String()
}

// Unwrap exposes the wrapped cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

// KindOf reports the Kind carried by err, or KindUnknown when err carries no engine
// error. Callers check the error first; asking a nil error for its kind yields
// KindUnknown rather than a special value.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindUnknown
}

// newError builds an engine error for a failure the engine detected itself, where the
// kind is already known.
func newError(kind Kind, op, rawURL string, err error) *Error {
	return &Error{Kind: kind, Op: op, URL: rawURL, Err: err}
}

// classify maps a transport failure to a stable Kind. err must not be nil: classify is
// only ever called from an error branch, and returning a typed nil here would produce
// an error value that is non-nil to its caller.
func classify(op, rawURL string, err error) *Error {
	// http.Client wraps every failure in *url.Error, so borrow its URL when the caller
	// has none of its own.
	if rawURL == "" {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			rawURL = uerr.URL
		}
	}
	return newError(classifyKind(err), op, rawURL, err)
}

// classifyKind attributes err to a Kind. The order is deliberate: how the request ended
// (cancelled, timed out, gave up on redirects) outranks where it failed, so a cancelled
// request is never reported as a connection problem.
func classifyKind(err error) Kind {
	switch {
	case errors.Is(err, ErrTooManyRedirects):
		return KindTooManyRedirects
	case errors.Is(err, context.Canceled):
		return KindCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return KindTimeout
	}

	// A failed lookup is reported as DNS even when it timed out: the actionable part is
	// the hostname or the resolver, not the clock.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return KindDNS
	}
	if isTLSError(err) {
		return KindTLS
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, wsaeconnrefused) {
		return KindConnRefused
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return KindTimeout
	}

	// Anything still socket-shaped — a reset, an unreachable network, a body that ended
	// mid-stream — is a connection failure rather than an unknown one.
	var opErr *net.OpError
	if errors.As(err, &opErr) || errors.Is(err, io.ErrUnexpectedEOF) {
		return KindConnection
	}
	return KindUnknown
}

// isTLSError reports whether err comes from the TLS handshake or from certificate
// verification. Certificate problems surface as x509 errors, a plaintext reply to an
// HTTPS request surfaces as a record-header error, and a peer that rejects the
// handshake surfaces as an alert.
func isTLSError(err error) bool {
	var (
		verify    *tls.CertificateVerificationError
		record    tls.RecordHeaderError
		alert     tls.AlertError
		authority x509.UnknownAuthorityError
		hostname  x509.HostnameError
		invalid   x509.CertificateInvalidError
	)
	return errors.As(err, &verify) ||
		errors.As(err, &record) ||
		errors.As(err, &alert) ||
		errors.As(err, &authority) ||
		errors.As(err, &hostname) ||
		errors.As(err, &invalid)
}
