package httpengine

import (
	"context"
	"crypto/tls"
	"net/http/httptrace"
	"sync"
	"time"

	"github.com/gsoares85/quiver/internal/model"
)

// clock reads the current time. The engine is the one place allowed to read the wall
// clock, and the source is injected so tests can script an exact timeline instead of
// asserting against whatever the machine happened to do.
type clock func() time.Time

// timings accumulates the connection lifecycle timestamps httptrace reports. The hooks
// fire on transport goroutines — several of them concurrently, when the dialer races a
// dual-stack connection — so every field is guarded by the mutex.
type timings struct {
	now clock

	mu           sync.Mutex
	start        time.Time // when the request was handed to the client
	dns          time.Duration
	connect      time.Duration
	tlsHandshake time.Duration
	dnsStart     time.Time
	connectStart time.Time
	tlsStart     time.Time
	firstByte    time.Time // last GotFirstResponseByte: the final hop's response
	connReused   bool
	remoteAddr   string
}

func newTimings(now clock) *timings {
	return &timings{now: now}
}

// withTiming attaches a trace to ctx that records the request's connection lifecycle
// into t, and marks the request as starting now. The returned context must be the one
// the request is sent with.
func withTiming(ctx context.Context, t *timings) context.Context {
	t.begin()
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { t.mark(&t.dnsStart) },
		DNSDone:              func(httptrace.DNSDoneInfo) { t.close(&t.dns, &t.dnsStart) },
		ConnectStart:         func(_, _ string) { t.mark(&t.connectStart) },
		ConnectDone:          func(_, _ string, _ error) { t.close(&t.connect, &t.connectStart) },
		TLSHandshakeStart:    func() { t.mark(&t.tlsStart) },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { t.close(&t.tlsHandshake, &t.tlsStart) },
		GotConn:              t.gotConn,
		GotFirstResponseByte: t.gotFirstByte,
	})
}

// begin marks the instant the request leaves for the transport; every other phase is
// measured relative to it.
func (t *timings) begin() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.start = t.now()
}

// mark records the start of a phase, keeping the earliest start while the phase is open.
// The dialer may race several connections at once (dual-stack Happy Eyeballs), and the
// honest span for that is from the first attempt to the one that succeeded.
func (t *timings) mark(start *time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if start.IsZero() {
		*start = t.now()
	}
}

// close ends a phase and accumulates its duration. Phases are summed rather than
// replaced because a redirect chain resolves, dials, and shakes hands once per hop; a
// late duplicate completion (the losing half of a raced dial) finds the phase already
// closed and is ignored.
func (t *timings) close(total *time.Duration, start *time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if start.IsZero() {
		return
	}
	*total += gap(*start, t.now())
	*start = time.Time{}
}

// gotConn records how the connection was obtained. It fires once per hop, so the last
// call describes the connection the final response arrived on.
func (t *timings) gotConn(info httptrace.GotConnInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connReused = info.Reused
	if info.Conn != nil {
		if addr := info.Conn.RemoteAddr(); addr != nil {
			t.remoteAddr = addr.String()
		}
	}
}

// gotFirstByte records the response's first byte. Like gotConn it fires per hop, and the
// last call is the one that matters: time to first byte means the response the caller
// actually receives, not an intermediate redirect.
func (t *timings) gotFirstByte() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.firstByte = t.now()
}

// conn reports how the final connection was obtained, for the response trace.
func (t *timings) conn() (reused bool, remoteAddr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connReused, t.remoteAddr
}

// result samples the end of the exchange and assembles the per-phase durations plus the
// total elapsed time. Call it once the body has been fully read, or once the request has
// failed.
//
// A zero phase means the phase did not happen: a reused connection performs no lookup,
// dial, or handshake, and an IP literal needs no DNS. That is a fact worth showing, not
// a gap to hide.
func (t *timings) result() (model.Timing, model.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	end := t.now()
	timing := model.Timing{
		DNS:     model.Duration(t.dns),
		Connect: model.Duration(t.connect),
		TLS:     model.Duration(t.tlsHandshake),
	}
	if !t.firstByte.IsZero() {
		timing.TTFB = model.Duration(gap(t.start, t.firstByte))
		timing.Download = model.Duration(gap(t.firstByte, end))
	}
	return timing, model.Duration(gap(t.start, end))
}

// gap is the distance between two instants, treating an unset or out-of-order pair as
// zero: a phase that never ran, or a clock that stepped backwards, must not surface as a
// negative duration.
func gap(start, end time.Time) time.Duration {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start)
}
