package httpengine

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"testing"
	"time"

	"github.com/gsoares85/quiver/internal/model"
	"github.com/stretchr/testify/require"
)

// fakeClock is a hand-driven time source: tests advance it explicitly, so phase
// durations are exact rather than whatever the machine happened to do.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{at: time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// tracedTimings wires a timings to a scripted clock and hands back the trace hooks the
// transport would call, so a whole connection lifecycle can be replayed deterministically.
func tracedTimings(t *testing.T) (*timings, *fakeClock, *httptrace.ClientTrace) {
	t.Helper()
	clk := newFakeClock()
	tm := newTimings(clk.now)
	ctx := withTiming(context.Background(), tm)

	trace := httptrace.ContextClientTrace(ctx)
	require.NotNil(t, trace, "withTiming must attach a client trace to the context")
	return tm, clk, trace
}

func TestTimingRecordsEveryPhase(t *testing.T) {
	tm, clk, trace := tracedTimings(t)

	trace.DNSStart(httptrace.DNSStartInfo{Host: "api.example.com"})
	clk.advance(10 * time.Millisecond)
	trace.DNSDone(httptrace.DNSDoneInfo{})

	trace.ConnectStart("tcp", "93.184.216.34:443")
	clk.advance(20 * time.Millisecond)
	trace.ConnectDone("tcp", "93.184.216.34:443", nil)

	trace.TLSHandshakeStart()
	clk.advance(30 * time.Millisecond)
	trace.TLSHandshakeDone(tls.ConnectionState{}, nil)

	trace.GotConn(httptrace.GotConnInfo{Conn: fakeConn{remote: "93.184.216.34:443"}})
	clk.advance(40 * time.Millisecond) // the server thinking
	trace.GotFirstResponseByte()
	clk.advance(50 * time.Millisecond) // streaming the body

	timing, total := tm.result()
	require.Equal(t, model.Duration(10*time.Millisecond), timing.DNS)
	require.Equal(t, model.Duration(20*time.Millisecond), timing.Connect)
	require.Equal(t, model.Duration(30*time.Millisecond), timing.TLS)
	require.Equal(t, model.Duration(100*time.Millisecond), timing.TTFB, "ttfb runs from the request start")
	require.Equal(t, model.Duration(50*time.Millisecond), timing.Download)
	require.Equal(t, model.Duration(150*time.Millisecond), total)

	reused, addr := tm.conn()
	require.False(t, reused)
	require.Equal(t, "93.184.216.34:443", addr)
}

func TestTimingReusedConnectionLeavesPhasesZero(t *testing.T) {
	tm, clk, trace := tracedTimings(t)

	// A pooled connection performs no lookup, dial, or handshake.
	trace.GotConn(httptrace.GotConnInfo{Reused: true, WasIdle: true, Conn: fakeConn{remote: "10.0.0.1:443"}})
	clk.advance(5 * time.Millisecond)
	trace.GotFirstResponseByte()
	clk.advance(3 * time.Millisecond)

	timing, total := tm.result()
	require.Zero(t, timing.DNS)
	require.Zero(t, timing.Connect)
	require.Zero(t, timing.TLS)
	require.Equal(t, model.Duration(5*time.Millisecond), timing.TTFB)
	require.Equal(t, model.Duration(3*time.Millisecond), timing.Download)
	require.Equal(t, model.Duration(8*time.Millisecond), total)

	reused, addr := tm.conn()
	require.True(t, reused)
	require.Equal(t, "10.0.0.1:443", addr)
}

func TestTimingAccumulatesAcrossRedirectHops(t *testing.T) {
	tm, clk, trace := tracedTimings(t)

	// Hop one: resolve, dial, respond with a redirect.
	trace.DNSStart(httptrace.DNSStartInfo{})
	clk.advance(10 * time.Millisecond)
	trace.DNSDone(httptrace.DNSDoneInfo{})
	trace.ConnectStart("tcp", "a:80")
	clk.advance(20 * time.Millisecond)
	trace.ConnectDone("tcp", "a:80", nil)
	trace.GotConn(httptrace.GotConnInfo{Conn: fakeConn{remote: "a:80"}})
	clk.advance(5 * time.Millisecond)
	trace.GotFirstResponseByte()

	// Hop two: a different host, so it resolves and dials again.
	clk.advance(1 * time.Millisecond)
	trace.DNSStart(httptrace.DNSStartInfo{})
	clk.advance(7 * time.Millisecond)
	trace.DNSDone(httptrace.DNSDoneInfo{})
	trace.ConnectStart("tcp", "b:80")
	clk.advance(13 * time.Millisecond)
	trace.ConnectDone("tcp", "b:80", nil)
	trace.GotConn(httptrace.GotConnInfo{Conn: fakeConn{remote: "b:80"}})
	clk.advance(4 * time.Millisecond)
	trace.GotFirstResponseByte()
	clk.advance(2 * time.Millisecond)

	timing, total := tm.result()
	require.Equal(t, model.Duration(17*time.Millisecond), timing.DNS, "both lookups count")
	require.Equal(t, model.Duration(33*time.Millisecond), timing.Connect, "both dials count")
	require.Equal(t, model.Duration(60*time.Millisecond), timing.TTFB,
		"ttfb runs to the final hop's first byte, not the redirect's")
	require.Equal(t, model.Duration(2*time.Millisecond), timing.Download)
	require.Equal(t, model.Duration(62*time.Millisecond), total)

	_, addr := tm.conn()
	require.Equal(t, "b:80", addr, "the trace describes the connection the response arrived on")
}

func TestTimingKeepsTheEarliestStartOfRacedDials(t *testing.T) {
	tm, clk, trace := tracedTimings(t)

	// Dual-stack dialing races two connections; the honest span runs from the first
	// attempt to the one that won.
	trace.ConnectStart("tcp", "[::1]:443")
	clk.advance(5 * time.Millisecond)
	trace.ConnectStart("tcp", "127.0.0.1:443")
	clk.advance(15 * time.Millisecond)
	trace.ConnectDone("tcp", "127.0.0.1:443", nil)
	clk.advance(100 * time.Millisecond)
	trace.ConnectDone("tcp", "[::1]:443", context.DeadlineExceeded) // the loser finishes late

	timing, _ := tm.result()
	require.Equal(t, model.Duration(20*time.Millisecond), timing.Connect)
}

func TestTimingIgnoresUnmatchedAndBackwardsHooks(t *testing.T) {
	tm, clk, trace := tracedTimings(t)

	trace.DNSDone(httptrace.DNSDoneInfo{})             // never started
	trace.TLSHandshakeDone(tls.ConnectionState{}, nil) // never started
	clk.advance(9 * time.Millisecond)

	timing, total := tm.result()
	require.Zero(t, timing.DNS)
	require.Zero(t, timing.TLS)
	require.Zero(t, timing.TTFB, "a request that never got a byte reports no ttfb")
	require.Zero(t, timing.Download)
	require.Equal(t, model.Duration(9*time.Millisecond), total, "a failed request still took time")
}

func TestTimingClampsABackwardsClock(t *testing.T) {
	tm, clk, trace := tracedTimings(t)

	trace.DNSStart(httptrace.DNSStartInfo{})
	clk.advance(-10 * time.Millisecond) // a clock adjustment mid-request
	trace.DNSDone(httptrace.DNSDoneInfo{})
	trace.GotFirstResponseByte()

	timing, total := tm.result()
	require.Zero(t, timing.DNS, "a phase must never report a negative duration")
	require.Zero(t, timing.TTFB)
	require.Zero(t, total)
}

func TestTimingHooksAreRaceFree(t *testing.T) {
	// The hooks fire on transport goroutines; -race turns any missed lock into a failure.
	tm, clk, trace := tracedTimings(t)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			trace.DNSStart(httptrace.DNSStartInfo{})
			clk.advance(time.Millisecond)
			trace.DNSDone(httptrace.DNSDoneInfo{})
			trace.ConnectStart("tcp", "a:80")
			trace.ConnectDone("tcp", "a:80", nil)
			trace.GotConn(httptrace.GotConnInfo{Conn: fakeConn{remote: "a:80"}})
			trace.GotFirstResponseByte()
			tm.conn()
			tm.result()
		}()
	}
	wg.Wait()

	timing, total := tm.result()
	require.GreaterOrEqual(t, timing.DNS, model.Duration(0))
	require.GreaterOrEqual(t, total, model.Duration(0))
}

// fakeConn is a net.Conn that only knows its remote address, which is all GotConn reads.
type fakeConn struct {
	net.Conn
	remote string
}

func (c fakeConn) RemoteAddr() net.Addr { return fakeAddr(c.remote) }

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

func TestTimingWithNilConn(t *testing.T) {
	tm, _, trace := tracedTimings(t)
	trace.GotConn(httptrace.GotConnInfo{}) // no connection attached

	reused, addr := tm.conn()
	require.False(t, reused)
	require.Empty(t, addr)
}

func TestTimingAgainstARealConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	get := func() (model.Timing, model.Duration, *timings) {
		tm := newTimings(time.Now)
		ctx := withTiming(context.Background(), tm)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		require.NoError(t, err)

		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer func() { require.NoError(t, resp.Body.Close()) }()
		_, err = io.ReadAll(resp.Body)
		require.NoError(t, err)

		timing, total := tm.result()
		return timing, total, tm
	}

	// A fresh connection dials; 127.0.0.1 needs no lookup and plain HTTP no handshake.
	timing, total, tm := get()
	require.Positive(t, timing.Connect, "a first request must dial")
	require.Zero(t, timing.DNS, "an ip literal is not resolved")
	require.Zero(t, timing.TLS, "plain http performs no handshake")
	require.Positive(t, timing.TTFB)
	require.GreaterOrEqual(t, total, timing.TTFB)
	require.GreaterOrEqual(t, timing.TTFB+timing.Download, model.Duration(0))

	reused, addr := tm.conn()
	require.False(t, reused)
	require.NotEmpty(t, addr)

	// The second request rides the pooled connection, so it dials nothing.
	timing, _, tm = get()
	require.Zero(t, timing.Connect, "a pooled connection is not dialled again")
	require.Positive(t, timing.TTFB)
	reused, _ = tm.conn()
	require.True(t, reused)
}

func TestTimingAgainstATLSConnection(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	tm := newTimings(time.Now)
	ctx := withTiming(context.Background(), tm)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, err := srv.Client().Do(req) // the test server's client trusts its certificate
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)

	timing, total := tm.result()
	require.Positive(t, timing.Connect)
	require.Positive(t, timing.TLS, "an https request must record its handshake")
	require.GreaterOrEqual(t, total, timing.TTFB)
}
