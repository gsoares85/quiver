package httpengine

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gsoares85/quiver/internal/model"
)

// defaultMaxRedirects applies when a request opts into following redirects without
// saying how many to allow. It matches the standard library's own limit.
const defaultMaxRedirects = 10

// Trace describes how a response was obtained: the redirect chain that led to it, the
// protocol and TLS the connection negotiated, and whether the connection came from the
// pool. It is run-time detail only, deliberately kept out of model.Response — that type
// is the versioned storage contract, and widening it for data nobody saves would cost a
// schema migration.
type Trace struct {
	Redirects  []RedirectHop  `json:"redirects,omitempty"`
	Proto      string         `json:"proto,omitempty"` // empty when no response arrived
	TLS        *TLSInfo       `json:"tls,omitempty"`
	Trailers   []model.Header `json:"trailers,omitempty"` // headers that followed the body
	ConnReused bool           `json:"connReused"`
	RemoteAddr string         `json:"remoteAddr,omitempty"`
	Duration   model.Duration `json:"duration"` // how long the exchange took, success or not
}

// RedirectHop is one link in a followed redirect chain.
type RedirectHop struct {
	Status int    `json:"status"` // the 3xx that caused the hop
	From   string `json:"from"`
	To     string `json:"to"`
}

// TLSInfo summarizes the negotiated TLS connection.
type TLSInfo struct {
	Version     string `json:"version"`              // e.g. "TLS 1.3"
	CipherSuite string `json:"cipherSuite"`          // e.g. "TLS_AES_128_GCM_SHA256"
	ServerName  string `json:"serverName,omitempty"` // the SNI sent; empty for an IP literal
}

// newTransport returns the engine's shared transport: pooled keep-alive connections,
// HTTP/2 where the server offers it, and the environment's proxy settings. It mirrors
// http.DefaultTransport, written out so every default the engine relies on is visible,
// with a larger per-host idle pool because an API client hammers a handful of hosts
// rather than thousands.
func newTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// redirectRecorder collects the chain a request followed. The client calls CheckRedirect
// on the same goroutine that called Do, and always before Do returns, so the slice needs
// no locking.
type redirectRecorder struct {
	hops []RedirectHop
}

// add records the redirect that produced req.
func (r *redirectRecorder) add(req *http.Request) {
	hop := RedirectHop{To: req.URL.String()}
	// The client attaches the response that caused this request, which is where the
	// status and the previous URL come from.
	if prev := req.Response; prev != nil {
		hop.Status = prev.StatusCode
		if prev.Request != nil && prev.Request.URL != nil {
			hop.From = prev.Request.URL.String()
		}
	}
	r.hops = append(r.hops, hop)
}

// buildClient returns the client policy for a single request: the engine's shared
// transport plus this request's redirect rules.
//
// Client.Timeout is deliberately left at zero. The deadline is enforced through the
// context instead, because Client.Timeout would also cap the time spent reading the
// body — which is precisely what must stay open and cancellable while a response
// streams.
func buildClient(transport http.RoundTripper, settings model.RequestSettings, rec *redirectRecorder) *http.Client {
	maxRedirects := settings.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !settings.FollowRedirects {
				// Hand the 3xx back as the response so its Location is there to inspect,
				// rather than silently chasing somewhere the user did not ask to go.
				return http.ErrUseLastResponse
			}
			if len(via) > maxRedirects {
				return fmt.Errorf("%w: stopped after %d", ErrTooManyRedirects, maxRedirects)
			}
			rec.add(req)
			return nil
		},
	}
}

// finishTrace closes out an exchange: it samples the clock once and describes how the
// request went — the hops it followed, the connection it used, how long it took, and, when
// a response did arrive, what that connection negotiated. resp is nil for a request that
// never got one, which is exactly why a failure can still be described.
//
// It returns the per-phase timing alongside the trace because sampling the clock twice
// would report two different totals for one request.
func finishTrace(t *timings, hops []RedirectHop, resp *http.Response) (*Trace, model.Timing) {
	timing, elapsed := t.result()
	reused, remoteAddr := t.conn()

	trace := &Trace{
		Redirects:  hops,
		ConnReused: reused,
		RemoteAddr: remoteAddr,
		Duration:   elapsed,
	}
	if resp == nil {
		return trace, timing
	}

	trace.Proto = resp.Proto
	if state := resp.TLS; state != nil {
		trace.TLS = &TLSInfo{
			Version:     tls.VersionName(state.Version),
			CipherSuite: tls.CipherSuiteName(state.CipherSuite),
			ServerName:  state.ServerName,
		}
	}
	// Trailers only carry values once the body has been read to the end, which is where
	// this is called from.
	if len(resp.Trailer) > 0 {
		trace.Trailers = headersFrom(resp.Trailer)
	}
	return trace, timing
}
