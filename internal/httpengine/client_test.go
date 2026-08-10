package httpengine

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gsoares85/quiver/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chainServer serves /hop/N, redirecting to /hop/N-1 down to /done, so a test can ask
// for a redirect chain of any length.
func chainServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/done", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("arrived"))
	})
	mux.HandleFunc("/hop/", func(w http.ResponseWriter, r *http.Request) {
		// assert rather than require: this runs on the server's goroutine, where FailNow
		// is unsupported.
		n, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
		assert.NoError(t, err)
		if n <= 1 {
			http.Redirect(w, r, "/done", http.StatusFound)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/hop/%d", n-1), http.StatusFound)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// do sends a GET through a client built for the given settings and returns the response
// along with the chain it followed.
func do(t *testing.T, rawURL string, settings model.RequestSettings) (*http.Response, []RedirectHop, error) {
	t.Helper()
	rec := &redirectRecorder{}
	client := buildClient(newTransport(), settings, rec)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	return resp, rec.hops, err
}

// readAll drains and closes a response body.
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { require.NoError(t, resp.Body.Close()) }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(data)
}

func TestClientDoesNotFollowRedirectsByDefault(t *testing.T) {
	srv := chainServer(t)

	resp, hops, err := do(t, srv.URL+"/hop/3", model.RequestSettings{})
	require.NoError(t, err)

	require.Equal(t, http.StatusFound, resp.StatusCode, "the 3xx itself is the response")
	require.Equal(t, "/hop/2", resp.Header.Get("Location"), "its Location is there to inspect")
	require.NotEqual(t, "arrived", readAll(t, resp))
	require.Empty(t, hops)
}

func TestClientFollowsRedirectsWhenAskedTo(t *testing.T) {
	srv := chainServer(t)

	resp, hops, err := do(t, srv.URL+"/hop/3", model.RequestSettings{
		FollowRedirects: true,
		MaxRedirects:    10,
	})
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "arrived", readAll(t, resp))
	require.Equal(t, []RedirectHop{
		{Status: http.StatusFound, From: srv.URL + "/hop/3", To: srv.URL + "/hop/2"},
		{Status: http.StatusFound, From: srv.URL + "/hop/2", To: srv.URL + "/hop/1"},
		{Status: http.StatusFound, From: srv.URL + "/hop/1", To: srv.URL + "/done"},
	}, hops, "every hop is recorded, in the order it was followed")
}

func TestClientEnforcesMaxRedirects(t *testing.T) {
	srv := chainServer(t)

	resp, hops, err := do(t, srv.URL+"/hop/5", model.RequestSettings{
		FollowRedirects: true,
		MaxRedirects:    2,
	})
	require.ErrorIs(t, err, ErrTooManyRedirects)
	require.Equal(t, KindTooManyRedirects, KindOf(classify("send request", "", err)))
	require.Len(t, hops, 2, "exactly MaxRedirects hops are followed before giving up")

	// A CheckRedirect failure hands back the last 3xx alongside the error, with its body
	// already closed: the engine must report the failure and never try to stream it.
	require.NotNil(t, resp)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	_, readErr := io.ReadAll(resp.Body)
	require.Error(t, readErr, "the client already closed the body it handed back")
}

func TestClientDefaultsMaxRedirectsWhenUnset(t *testing.T) {
	srv := chainServer(t)

	// A chain longer than the default proves the fallback is applied rather than treating
	// zero as "no redirects allowed" or "unlimited".
	_, hops, err := do(t, srv.URL+"/hop/20", model.RequestSettings{FollowRedirects: true})
	require.ErrorIs(t, err, ErrTooManyRedirects)
	require.Len(t, hops, defaultMaxRedirects)
}

func TestClientFollowsExactlyMaxRedirects(t *testing.T) {
	srv := chainServer(t)

	// Two redirects (/hop/2 -> /hop/1 -> /done) must fit in a budget of two.
	resp, hops, err := do(t, srv.URL+"/hop/2", model.RequestSettings{
		FollowRedirects: true,
		MaxRedirects:    2,
	})
	require.NoError(t, err)
	require.Equal(t, "arrived", readAll(t, resp))
	require.Len(t, hops, 2)
}

func TestClientDropsAuthorizationOnACrossHostRedirect(t *testing.T) {
	// The target records what it was sent, so the test can pin exactly which credentials
	// survive a redirect — which is narrower than "credentials do not leak", and worth
	// stating precisely because the difference is a real footgun.
	var got http.Header
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte("arrived"))
	}))
	defer target.Close()

	targetURL, err := url.Parse(target.URL)
	require.NoError(t, err)
	// Go compares hostnames, not host:port, when deciding whether a redirect leaves the
	// origin — so two httptest servers are both "127.0.0.1" and would prove nothing here.
	// Addressing the target as localhost makes it a genuinely different host while still
	// reaching the same listener. This is the one test that needs localhost to resolve to
	// the loopback interface.
	crossHost := "http://" + net.JoinHostPort("localhost", targetURL.Port()) + "/target"

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/same-host" {
			http.Redirect(w, r, "/landing", http.StatusFound)
			return
		}
		if r.URL.Path == "/landing" {
			got = r.Header.Clone()
			_, _ = w.Write([]byte("arrived"))
			return
		}
		http.Redirect(w, r, crossHost, http.StatusFound)
	}))
	defer origin.Close()

	send := func(path string) http.Header {
		got = nil
		rec := &redirectRecorder{}
		client := buildClient(newTransport(), model.RequestSettings{
			FollowRedirects: true,
			MaxRedirects:    5,
		}, rec)

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, origin.URL+path, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("X-Api-Key", "custom-header-key")
		req.Header.Set("X-Trace-Id", "abc")

		resp, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, "arrived", readAll(t, resp))
		return got
	}

	crossed := send("/cross-host")
	require.Empty(t, crossed.Get("Authorization"), "an Authorization header stops at the origin host")
	require.Equal(t, "abc", crossed.Get("X-Trace-Id"), "ordinary headers still travel")
	require.Equal(t, "custom-header-key", crossed.Get("X-Api-Key"),
		"an api key in a custom header is just a header: it does follow a redirect to another "+
			"host, which is why the readme says so rather than promising it does not")

	same := send("/same-host")
	require.Equal(t, "Bearer secret", same.Get("Authorization"),
		"a redirect within the same host keeps the credential")
}

func TestBuildClientLeavesTheDeadlineToTheContext(t *testing.T) {
	client := buildClient(newTransport(), model.RequestSettings{TimeoutMs: 1_000}, &redirectRecorder{})

	require.Zero(t, client.Timeout,
		"Client.Timeout would also cap body reads, which must stay open while a response streams")
	require.Nil(t, client.Jar, "a cookie jar is a workspace-level concern, not a per-request one")
}

func TestNewTransportDefaults(t *testing.T) {
	tr := newTransport()

	require.NotNil(t, tr.Proxy, "HTTP(S)_PROXY and NO_PROXY are honored by default")
	require.True(t, tr.ForceAttemptHTTP2)
	require.Equal(t, 8, tr.MaxIdleConnsPerHost, "an api client reuses connections to a few hosts")
	require.Equal(t, 90*time.Second, tr.IdleConnTimeout)
	require.Equal(t, 10*time.Second, tr.TLSHandshakeTimeout)
	require.Nil(t, tr.TLSClientConfig, "verification stays at the standard library's defaults")
}

func TestFinishTracePlainHTTP(t *testing.T) {
	srv := chainServer(t)

	resp, hops, err := do(t, srv.URL+"/done", model.RequestSettings{})
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()

	clk := newFakeClock()
	tm := newTimings(clk.now)
	tm.begin()
	clk.advance(7 * time.Millisecond)

	trace, _ := finishTrace(tm, hops, resp)
	require.Equal(t, "HTTP/1.1", trace.Proto)
	require.Nil(t, trace.TLS, "a plain connection negotiates no tls")
	require.False(t, trace.ConnReused)
	require.Empty(t, trace.Redirects)
	require.Empty(t, trace.Trailers)
	require.Equal(t, model.Duration(7*time.Millisecond), trace.Duration)
}

func TestFinishTraceWithoutAResponse(t *testing.T) {
	// A request that never got a response still has a story: how long it took, and how far
	// down a redirect chain it made it.
	clk := newFakeClock()
	tm := newTimings(clk.now)
	tm.begin()
	clk.advance(30 * time.Second)

	hops := []RedirectHop{{Status: 302, From: "https://a/x", To: "https://b/x"}}
	trace, _ := finishTrace(tm, hops, nil)

	require.Equal(t, model.Duration(30*time.Second), trace.Duration, "a failure reports how long it took")
	require.Equal(t, hops, trace.Redirects)
	require.Empty(t, trace.Proto, "nothing was negotiated")
	require.Nil(t, trace.TLS)
}

func TestFinishTraceCapturesTrailers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "X-Checksum")
		_, _ = w.Write([]byte("payload"))
		w.Header().Set("X-Checksum", "abc123") // set after the body: a real trailer
	}))
	defer srv.Close()

	resp, hops, err := do(t, srv.URL, model.RequestSettings{})
	require.NoError(t, err)
	require.Equal(t, "payload", readAll(t, resp)) // trailers only arrive once the body ends

	trace, _ := finishTrace(newTimings(newFakeClock().now), hops, resp)
	require.Equal(t, []model.Header{{Key: "X-Checksum", Value: "abc123", Enabled: true}}, trace.Trailers)
}

func TestFinishTraceTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secure"))
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()

	trace, _ := finishTrace(newTimings(time.Now), nil, resp)
	require.False(t, trace.ConnReused)
	require.NotNil(t, trace.TLS)
	require.Equal(t, "TLS 1.3", trace.TLS.Version)
	require.Contains(t, trace.TLS.CipherSuite, "TLS_")
}

func TestTransportNegotiatesHTTP2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("h2"))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	// The engine's own transport, taught to trust the test server's certificate, so this
	// proves the engine negotiates h2 rather than the test server's client doing it.
	tr := newTransport()
	tr.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	t.Cleanup(tr.CloseIdleConnections)

	rec := &redirectRecorder{}
	client := buildClient(tr, model.RequestSettings{}, rec)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, "h2", readAll(t, resp))

	trace, _ := finishTrace(newTimings(time.Now), rec.hops, resp)
	require.Equal(t, "HTTP/2.0", trace.Proto)
	require.NotNil(t, trace.TLS)
}
