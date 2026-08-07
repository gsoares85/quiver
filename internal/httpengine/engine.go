package httpengine

import (
	"context"
	"net/http"
	"time"

	"github.com/gsoares85/quiver/internal/model"
)

// opSendRequest labels failures that happen once the request is on the wire.
const opSendRequest = "send request"

// Engine executes requests. Implementations are safe for concurrent use.
type Engine interface {
	// Execute runs a fully-resolved request (variables already substituted) and streams
	// response chunks on the returned channel. The channel is closed when the response
	// completes or ctx is cancelled. The final chunk carries the assembled
	// model.Response.
	Execute(ctx context.Context, req model.Request) (<-chan Chunk, error)
}

// Chunk is one streamed piece of a response. Exactly one of Meta, Body, Done, and Err is
// set; Trace accompanies the terminal Done chunk.
type Chunk struct {
	Meta  *model.Response // status and headers are known; the body is still streaming
	Body  []byte          // a slice of body bytes, in order
	Done  *model.Response // terminal: the assembled response, with timing
	Err   error           // terminal: the request failed (never set together with Done)
	Trace *Trace          // how the response was obtained; set alongside Done
}

// httpEngine is the HTTP implementation of Engine. Its transport is shared across every
// request so connections pool and stay alive.
type httpEngine struct {
	transport http.RoundTripper
	now       clock
	opener    FileOpener
}

// Option customizes an Engine.
type Option func(*httpEngine)

// WithTransport replaces the shared transport. It is the seam for anything the transport
// owns that the request model does not yet express — client certificates, a custom proxy,
// or a relaxed TLS configuration for local development.
func WithTransport(transport http.RoundTripper) Option {
	return func(e *httpEngine) { e.transport = transport }
}

// WithClock replaces the time source used for timing. Tests inject a scripted clock; in
// production this is time.Now.
func WithClock(now func() time.Time) Option {
	return func(e *httpEngine) { e.now = now }
}

// WithFileOpener replaces how referenced upload files are read. It keeps the engine's
// only filesystem authority explicit and injectable.
func WithFileOpener(opener FileOpener) Option {
	return func(e *httpEngine) { e.opener = opener }
}

// New returns an Engine ready for concurrent use by the desktop app and the CLI alike.
func New(opts ...Option) Engine {
	e := &httpEngine{
		transport: newTransport(),
		now:       time.Now,
		opener:    osFileOpener{},
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Execute builds the wire request and streams the response.
//
// A request that cannot be attempted at all fails here, synchronously, with a nil
// channel: an unknown method, an unusable URL, unflattened auth, or an unreadable body
// file is a caller bug, and reporting it the same way as a network failure would send
// front-ends looking at the wrong thing. Everything that fails once the request is on the
// wire arrives as the terminal Err chunk instead.
func (e *httpEngine) Execute(ctx context.Context, req model.Request) (<-chan Chunk, error) {
	hr, err := newRequest(ctx, req, e.opener)
	if err != nil {
		return nil, err
	}

	out := make(chan Chunk, 1)
	go e.run(ctx, hr, req.Settings, out)
	return out, nil
}

// run performs the exchange and streams it to out, closing the channel on the way out.
func (e *httpEngine) run(ctx context.Context, hr *http.Request, settings model.RequestSettings, out chan<- Chunk) {
	defer close(out)

	// The deadline lives on the context rather than on the client so that it also covers
	// reading the body, and so cancelling stops a stream that is already flowing.
	if settings.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(settings.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	timings := newTimings(e.now)
	ctx = withTiming(ctx, timings)
	hr = hr.WithContext(ctx)

	redirects := &redirectRecorder{}
	resp, err := buildClient(e.transport, settings, redirects).Do(hr)
	if err != nil {
		// A CheckRedirect failure hands back the last response with its body already
		// closed, so there is nothing here to stream — only a failure to report.
		sendFinal(ctx, out, Chunk{Err: classify(opSendRequest, hr.URL.String(), err)})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	meta := metaFrom(resp)
	if !send(ctx, out, Chunk{Meta: meta}) {
		return
	}

	// resp.ContentLength is -1 when the length is unknown, including whenever the
	// transport decompressed the body, so the hint is never misleading.
	body, size, err := streamBody(ctx, resp.Body, resp.ContentLength, out)
	if err != nil {
		// The chunks that did arrive have already been delivered; this ends the stream.
		sendFinal(ctx, out, Chunk{Err: classify(opReadBody, hr.URL.String(), err)})
		return
	}

	timing, total := timings.result()
	reused, remoteAddr := timings.conn()
	done := *meta
	done.Body = body
	done.Size = size
	done.Duration = total
	done.Timing = timing

	sendFinal(ctx, out, Chunk{
		Done:  &done,
		Trace: traceFrom(resp, redirects.hops, reused, remoteAddr),
	})
}

// send delivers a chunk unless the caller has gone away, reporting whether it landed.
// Every send selects on the context so an abandoned stream can never wedge the engine's
// goroutine on a channel nobody is reading.
func send(ctx context.Context, out chan<- Chunk, chunk Chunk) bool {
	select {
	case out <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

// sendFinal delivers the terminal chunk. Unlike send it does not give up just because the
// context is done: a caller who cancelled still deserves to be told why the stream ended,
// and that is the common case behind a stop button. It never blocks on a caller that has
// stopped reading — then the closed channel is the only signal left.
func sendFinal(ctx context.Context, out chan<- Chunk, chunk Chunk) {
	select {
	case out <- chunk:
	case <-ctx.Done():
		select {
		case out <- chunk: // the buffer still has room, so hand it over anyway
		default:
		}
	}
}
