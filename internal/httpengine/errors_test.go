package httpengine

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// allKinds lists every classification the engine can report. Tests iterate it so a new
// kind cannot be added without wording and a stable string.
var allKinds = []Kind{
	KindRequest,
	KindUnsupported,
	KindDNS,
	KindConnRefused,
	KindConnection,
	KindTLS,
	KindTimeout,
	KindCanceled,
	KindTooManyRedirects,
	KindUnknown,
}

// TestKindStringsAreStable locks the wire strings: front-ends match on them and the CLI
// maps them to exit codes, so changing one is a breaking change, not a rename.
func TestKindStringsAreStable(t *testing.T) {
	want := map[Kind]string{
		KindRequest:          "request",
		KindUnsupported:      "unsupported",
		KindDNS:              "dns",
		KindConnRefused:      "connection_refused",
		KindConnection:       "connection",
		KindTLS:              "tls",
		KindTimeout:          "timeout",
		KindCanceled:         "canceled",
		KindTooManyRedirects: "too_many_redirects",
		KindUnknown:          "unknown",
	}
	require.Len(t, want, len(allKinds))
	for kind, s := range want {
		require.Equal(t, s, string(kind))
	}
}

func TestKindText(t *testing.T) {
	require.Len(t, kindText, len(allKinds), "kindText has an entry per kind and no stale ones")
	for _, kind := range allKinds {
		require.Containsf(t, kindText, kind, "kind %q has no wording", kind)
		require.NotEmpty(t, kind.text())
	}

	// An unlisted kind degrades to its raw value, and the zero value reads as unknown.
	require.Equal(t, "teapot", Kind("teapot").text())
	require.Equal(t, kindText[KindUnknown], Kind("").text())
}

func TestClassifyKind(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Kind
	}{
		{
			name: "redirect limit",
			err:  &url.Error{Op: "Get", URL: "https://x", Err: fmt.Errorf("stopped: %w", ErrTooManyRedirects)},
			want: KindTooManyRedirects,
		},
		{
			name: "caller cancelled",
			err:  &url.Error{Op: "Get", URL: "https://x", Err: context.Canceled},
			want: KindCanceled,
		},
		{
			name: "deadline exceeded",
			err:  &url.Error{Op: "Get", URL: "https://x", Err: context.DeadlineExceeded},
			want: KindTimeout,
		},
		{
			name: "host not found",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "nope.invalid", IsNotFound: true}},
			want: KindDNS,
		},
		{
			name: "lookup timeout is still dns",
			err:  &net.DNSError{Err: "i/o timeout", Name: "slow.invalid", IsTimeout: true},
			want: KindDNS,
		},
		{
			name: "certificate verification",
			err:  &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
			want: KindTLS,
		},
		{
			name: "plaintext reply to https",
			err:  tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"},
			want: KindTLS,
		},
		{
			name: "handshake alert",
			err:  tls.AlertError(80),
			want: KindTLS,
		},
		{
			name: "unknown authority",
			err:  x509.UnknownAuthorityError{},
			want: KindTLS,
		},
		{
			name: "hostname mismatch",
			err:  x509.HostnameError{Certificate: &x509.Certificate{}, Host: "example.com"},
			want: KindTLS,
		},
		{
			name: "expired certificate",
			err:  x509.CertificateInvalidError{Cert: &x509.Certificate{}, Reason: x509.Expired},
			want: KindTLS,
		},
		{
			name: "connection refused",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
			want: KindConnRefused,
		},
		{
			// Windows reports WSAECONNREFUSED instead; classification must not differ.
			name: "connection refused on windows",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: wsaeconnrefused},
			want: KindConnRefused,
		},
		{
			name: "socket deadline",
			err:  &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded},
			want: KindTimeout,
		},
		{
			name: "connection reset",
			err:  &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET},
			want: KindConnection,
		},
		{
			name: "truncated body",
			err:  io.ErrUnexpectedEOF,
			want: KindConnection,
		},
		{
			name: "unattributable",
			err:  errors.New("boom"),
			want: KindUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, classifyKind(tt.err))
			require.Equal(t, tt.want, KindOf(classify("send request", "", tt.err)))
		})
	}
}

func TestClassifyKeepsContextAndCause(t *testing.T) {
	cause := &net.DNSError{Err: "no such host", Name: "nope.invalid", IsNotFound: true}
	wrapped := &url.Error{Op: "Get", URL: "https://nope.invalid/v1", Err: cause}

	err := classify("send request", "", wrapped)
	require.Equal(t, KindDNS, err.Kind)
	require.Equal(t, "send request", err.Op)
	require.Equal(t, "https://nope.invalid/v1", err.URL, "url is borrowed from the *url.Error")

	// The cause stays reachable for logs and for callers that want the detail.
	var dns *net.DNSError
	require.ErrorAs(t, err, &dns)
	require.Equal(t, "nope.invalid", dns.Name)
	require.ErrorIs(t, err, cause)

	// An explicit URL wins over the one the client wrapped in.
	err = classify("send request", "https://explicit.example/v2", wrapped)
	require.Equal(t, "https://explicit.example/v2", err.URL)
}

func TestErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		err  *Error
		want string
	}{
		{
			name: "op, url, and cause",
			err:  newError(KindConnRefused, "send request", "https://x/v1", errors.New("dial tcp 127.0.0.1:1: connect: connection refused")),
			want: "send request https://x/v1: connection refused: dial tcp 127.0.0.1:1: connect: connection refused",
		},
		{
			name: "op only",
			err:  newError(KindRequest, "build request", "", nil),
			want: "build request: invalid request",
		},
		{
			name: "url only",
			err:  newError(KindTimeout, "", "https://x/v1", nil),
			want: "https://x/v1: timed out",
		},
		{
			name: "kind only",
			err:  newError(KindUnknown, "", "", nil),
			want: "request failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.err.Error())
		})
	}
}

func TestKindOf(t *testing.T) {
	require.Equal(t, KindTLS, KindOf(fmt.Errorf("run request: %w", newError(KindTLS, "send request", "https://x", nil))))
	require.Equal(t, KindUnknown, KindOf(errors.New("not an engine error")))
	require.Equal(t, KindUnknown, KindOf(nil))
}

func TestErrorUnwrapsToNil(t *testing.T) {
	// An engine-detected rejection carries no cause; unwrapping must not panic and must
	// simply end the chain.
	err := newError(KindRequest, "build request", "://bad", nil)
	require.NoError(t, errors.Unwrap(err))
	require.NotErrorIs(t, err, io.EOF)
}
