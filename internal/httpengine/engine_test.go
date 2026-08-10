package httpengine

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoares85/quiver/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stream is a collected execution: the chunks in the order they arrived, folded into the
// pieces a caller cares about.
type stream struct {
	metas  []*model.Response
	body   []byte
	chunks int
	done   *model.Response
	trace  *Trace
	err    error
}

// collect drains a channel to completion and folds it into a stream, asserting the
// channel closes.
func collect(t *testing.T, ch <-chan Chunk) stream {
	t.Helper()
	var got stream
	for chunk := range ch {
		switch {
		case chunk.Meta != nil:
			got.metas = append(got.metas, chunk.Meta)
		case chunk.Body != nil:
			require.NotNil(t, got.metas, "a body chunk must never precede the metadata")
			got.body = append(got.body, chunk.Body...)
			got.chunks++
		case chunk.Done != nil:
			got.done = chunk.Done
			got.trace = chunk.Trace
		case chunk.Err != nil:
			got.err = chunk.Err
			got.trace = chunk.Trace
		}
	}
	return got
}

// execute runs a request through a fresh engine and collects the whole stream.
func execute(t *testing.T, ctx context.Context, req model.Request, opts ...Option) stream {
	t.Helper()
	ch, err := New(opts...).Execute(ctx, req)
	require.NoError(t, err)
	return collect(t, ch)
}

func TestExecuteStreamsMetaThenBodyThenDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Rate-Limit", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello "))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("world"))
	}))
	defer srv.Close()

	got := execute(t, context.Background(), model.Request{Method: http.MethodGet, URL: srv.URL})

	require.NoError(t, got.err)
	require.Len(t, got.metas, 1, "exactly one metadata chunk")
	require.Equal(t, http.StatusOK, got.metas[0].Status)
	require.Equal(t, "OK", got.metas[0].StatusText)
	require.Equal(t, "hello world", string(got.body))

	require.NotNil(t, got.done)
	require.Equal(t, "hello world", string(got.done.Body))
	require.Equal(t, int64(len("hello world")), got.done.Size)
	require.Equal(t, http.StatusOK, got.done.Status)
	require.Positive(t, got.done.Duration)
	require.Positive(t, got.done.Timing.TTFB)
	require.GreaterOrEqual(t, got.done.Duration, got.done.Timing.TTFB)

	require.NotNil(t, got.trace)
	require.Equal(t, "HTTP/1.1", got.trace.Proto)
	require.Nil(t, got.trace.TLS)
	require.NotEmpty(t, got.trace.RemoteAddr)
	require.Empty(t, got.trace.Redirects)

	var contentType string
	for _, h := range got.done.Headers {
		if h.Key == "Content-Type" {
			contentType = h.Value
		}
	}
	require.Equal(t, "text/plain", contentType)
}

func TestExecuteSendsTheRequestFaithfully(t *testing.T) {
	type received struct {
		method string
		path   string
		query  string
		header http.Header
		body   string
	}
	var got received
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// assert, not require: FailNow off the test goroutine is unsupported, and aborting
		// a handler mid-response would surface as a confusing client-side error instead of
		// the assertion that actually failed.
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		got = received{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			header: r.Header.Clone(),
			body:   string(body),
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	out := execute(t, context.Background(), model.Request{
		Method:  http.MethodPost,
		URL:     srv.URL + "/v1/users?source=cli",
		Query:   []model.Param{{Key: "notify", Value: "true"}},
		Headers: []model.Header{{Key: "X-Trace-Id", Value: "abc"}},
		Body:    &model.Body{Type: model.BodyJSON, Text: `{"name":"ada"}`},
		Auth:    &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "tok-1"}},
	})

	require.NoError(t, out.err)
	require.Equal(t, http.StatusCreated, out.done.Status)

	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/v1/users", got.path)
	require.Equal(t, "source=cli&notify=true", got.query, "authored query order survives the wire")
	require.Equal(t, `{"name":"ada"}`, got.body)
	require.Equal(t, "application/json", got.header.Get("Content-Type"))
	require.Equal(t, "abc", got.header.Get("X-Trace-Id"))
	require.Equal(t, "Bearer tok-1", got.header.Get("Authorization"))
	require.Equal(t, fmt.Sprint(len(`{"name":"ada"}`)), got.header.Get("Content-Length"))
}

func TestExecuteRejectsUnusableRequestsSynchronously(t *testing.T) {
	tests := []struct {
		name string
		req  model.Request
		want Kind
	}{
		{
			name: "unknown method",
			req:  model.Request{Method: "FETCH", URL: "https://api.example.com/v1"},
			want: KindRequest,
		},
		{
			name: "another protocol",
			req:  model.Request{Method: "GRPC", URL: "https://api.example.com/v1"},
			want: KindUnsupported,
		},
		{
			name: "unresolved url",
			req:  model.Request{Method: http.MethodGet, URL: "{{baseUrl}}/users"},
			want: KindRequest,
		},
		{
			name: "auth still inherited",
			req: model.Request{
				Method: http.MethodGet,
				URL:    "https://api.example.com/v1",
				Auth:   &model.Auth{Type: model.AuthInherit},
			},
			want: KindRequest,
		},
		{
			name: "body file missing",
			req: model.Request{
				Method: http.MethodPost,
				URL:    "https://api.example.com/v1",
				Body:   &model.Body{Type: model.BodyBinary, Files: []model.FileRef{{Path: "/tmp/nope"}}},
			},
			want: KindRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, err := New().Execute(context.Background(), tt.req)
			require.Nil(t, ch, "nothing was attempted, so there is no stream to read")
			require.Equal(t, tt.want, KindOf(err))
		})
	}
}

func TestExecuteFollowsRedirectsAndReportsTheChain(t *testing.T) {
	srv := chainServer(t)

	got := execute(t, context.Background(), model.Request{
		Method:   http.MethodGet,
		URL:      srv.URL + "/hop/2",
		Settings: model.RequestSettings{FollowRedirects: true, MaxRedirects: 5},
	})

	require.NoError(t, got.err)
	require.Equal(t, "arrived", string(got.done.Body))
	require.Len(t, got.trace.Redirects, 2)
	require.Equal(t, srv.URL+"/hop/2", got.trace.Redirects[0].From)
	require.Equal(t, srv.URL+"/done", got.trace.Redirects[1].To)
}

func TestExecuteLosesAQueryApiKeyAcrossARedirect(t *testing.T) {
	// A redirect's next URL comes from the Location header, so nothing from the original
	// query string travels with it — including an api key placed there. The request arrives
	// unauthenticated, which is surprising enough to be worth pinning and documenting.
	var arrivedWith string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			arrivedWith = r.URL.RawQuery
			_, _ = w.Write([]byte("arrived"))
			return
		}
		http.Redirect(w, r, "/target", http.StatusFound)
	}))
	defer srv.Close()

	got := execute(t, context.Background(), model.Request{
		Method: http.MethodGet,
		URL:    srv.URL + "/start",
		Auth: &model.Auth{Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{
			Key: "api_key", Value: "k-1", In: "query",
		}},
		Settings: model.RequestSettings{FollowRedirects: true, MaxRedirects: 3},
	})

	require.NoError(t, got.err)
	require.Equal(t, "arrived", string(got.done.Body))
	require.Empty(t, arrivedWith, "the key did not survive the hop, and the readme says so")
}

func TestExecuteReportsARedirectLimit(t *testing.T) {
	srv := chainServer(t)

	got := execute(t, context.Background(), model.Request{
		Method:   http.MethodGet,
		URL:      srv.URL + "/hop/5",
		Settings: model.RequestSettings{FollowRedirects: true, MaxRedirects: 1},
	})

	require.Equal(t, KindTooManyRedirects, KindOf(got.err))
	require.Nil(t, got.done)
	require.Empty(t, got.metas, "a request that gave up never produced a response to describe")
}

func TestExecuteTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done(): // let the handler go as soon as the client hangs up
		}
	}))
	defer srv.Close()

	got := execute(t, context.Background(), model.Request{
		Method:   http.MethodGet,
		URL:      srv.URL,
		Settings: model.RequestSettings{TimeoutMs: 50},
	})

	require.Equal(t, KindTimeout, KindOf(got.err))
	require.Nil(t, got.done)
}

func TestExecuteStopsAStreamingResponseWhenCancelled(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("first"))
		w.(http.Flusher).Flush()
		select { // hold the response open, the way an event stream would
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := New().Execute(ctx, model.Request{Method: http.MethodGet, URL: srv.URL})
	require.NoError(t, err)

	// Read until the first body chunk arrives, then walk away mid-stream.
	var body []byte
	for chunk := range ch {
		if chunk.Body != nil {
			body = append(body, chunk.Body...)
			cancel()
			break
		}
	}
	require.Equal(t, "first", string(body))

	// The channel must still close, and a caller who cancelled is told why the stream
	// ended rather than being left to infer it from a closed channel.
	got := collect(t, ch)
	require.Nil(t, got.done, "a cancelled stream never completes")
	require.Equal(t, KindCanceled, KindOf(got.err))
}

// cancellingTransport answers a request and cancels the caller's context on the way out,
// which is the one moment that makes handing over the response metadata fail. Its body
// then reports the cancellation, as a real one would.
type cancellingTransport struct{ cancel context.CancelFunc }

func (t cancellingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.cancel()
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Proto:      "HTTP/1.1",
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       ctxBody{ctx: req.Context()},
		Request:    req,
	}, nil
}

// ctxBody is a response body that fails once its context is done.
type ctxBody struct{ ctx context.Context }

func (b ctxBody) Read([]byte) (int, error) { return 0, b.ctx.Err() }
func (b ctxBody) Close() error             { return nil }

func TestExecuteAlwaysEndsWithATerminalChunk(t *testing.T) {
	// Cancelling between the response arriving and the metadata being handed over used to
	// close the channel with no terminal chunk at all, leaving the caller to infer what
	// happened. Whether the metadata wins that race or not is decided by a select on two
	// ready cases, so the run is repeated: every attempt must end with exactly one Err
	// explaining itself, and over this many attempts both sides come up.
	for attempt := range 50 {
		ctx, cancel := context.WithCancel(context.Background())

		ch, err := New(WithTransport(cancellingTransport{cancel: cancel})).
			Execute(ctx, model.Request{Method: http.MethodGet, URL: "https://api.example.com/v1"})
		require.NoError(t, err)

		var terminals int
		var done *model.Response
		var failure error
		for chunk := range ch {
			switch {
			case chunk.Done != nil:
				done = chunk.Done
				terminals++
			case chunk.Err != nil:
				failure = chunk.Err
				terminals++
			}
		}
		cancel()

		require.Equalf(t, 1, terminals, "attempt %d: a stream ends once, and always says how", attempt)
		require.Nilf(t, done, "attempt %d: a cancelled request never completes", attempt)
		require.Equalf(t, KindCanceled, KindOf(failure), "attempt %d", attempt)
	}
}

func TestExecuteReportsARefusedConnection(t *testing.T) {
	// Take a port and immediately give it back, so nothing is listening on it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	got := execute(t, context.Background(), model.Request{
		Method: http.MethodGet,
		URL:    "http://" + addr + "/v1",
	})

	require.Equal(t, KindConnRefused, KindOf(got.err))
	require.Nil(t, got.done)
}

func TestExecuteReportsAPeerThatHangsUp(t *testing.T) {
	// A server that accepts the connection and closes it without answering — a crashed
	// backend, a port held by something that is not an HTTP server — reaches the transport
	// as a bare EOF with no socket error attached, so it is the one connection failure that
	// has to be recognised on its own rather than through *net.OpError.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	got := execute(t, context.Background(), model.Request{
		Method: http.MethodGet,
		URL:    "http://" + listener.Addr().String() + "/v1",
	})

	require.Equal(t, KindConnection, KindOf(got.err), "not an unattributable failure")
	require.Nil(t, got.done)
}

func TestExecuteWithAnEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	got := execute(t, context.Background(), model.Request{Method: http.MethodGet, URL: srv.URL})

	require.NoError(t, got.err)
	require.Zero(t, got.chunks, "no body means no body chunk")
	require.Equal(t, http.StatusNoContent, got.done.Status)
	require.Empty(t, got.done.Body)
	require.Zero(t, got.done.Size)
}

func TestExecuteHeadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "42")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got := execute(t, context.Background(), model.Request{Method: http.MethodHead, URL: srv.URL})

	require.NoError(t, got.err)
	require.Zero(t, got.chunks)
	require.Equal(t, http.StatusOK, got.done.Status)
	require.Empty(t, got.done.Body)
}

func TestExecuteOverHTTP2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("h2 payload"))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	transport := newTransport()
	transport.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	t.Cleanup(transport.CloseIdleConnections)

	got := execute(t, context.Background(),
		model.Request{Method: http.MethodGet, URL: srv.URL},
		WithTransport(transport))

	require.NoError(t, got.err)
	require.Equal(t, "h2 payload", string(got.done.Body))
	require.Equal(t, "HTTP/2.0", got.trace.Proto)
	require.NotNil(t, got.trace.TLS)
	require.Equal(t, "TLS 1.3", got.trace.TLS.Version)
}

func TestExecuteUsesTheInjectedClock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// A clock that never moves proves the engine reads time only through the injected
	// source: every measured phase collapses to zero.
	frozen := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	got := execute(t, context.Background(),
		model.Request{Method: http.MethodGet, URL: srv.URL},
		WithClock(func() time.Time { return frozen }))

	require.NoError(t, got.err)
	require.Zero(t, got.done.Duration)
	require.Equal(t, model.Timing{}, got.done.Timing)
}

func TestExecuteUsesTheInjectedFileOpener(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		got = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	opener := newFakeOpener(map[string]string{"/tmp/report.pdf": "%PDF payload"})
	out := execute(t, context.Background(), model.Request{
		Method: http.MethodPost,
		URL:    srv.URL,
		Body:   &model.Body{Type: model.BodyBinary, Files: []model.FileRef{{Path: "/tmp/report.pdf"}}},
	}, WithFileOpener(opener))

	require.NoError(t, out.err)
	require.Equal(t, "%PDF payload", got, "the upload came from the injected opener, not the disk")
}

func TestExecuteIsSafeForConcurrentUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Path))
	}))
	defer srv.Close()

	engine := New()
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := fmt.Sprintf("/v1/%d", i)
			ch, err := engine.Execute(context.Background(), model.Request{
				Method: http.MethodGet,
				URL:    srv.URL + path,
			})
			if !assert.NoError(t, err) { // assert: this is not the test goroutine
				return
			}

			got := collect(t, ch)
			assert.NoError(t, got.err)
			assert.Equal(t, path, string(got.done.Body), "responses must not cross wires")
		}()
	}
	wg.Wait()
}

func TestExecuteStreamsALargeBodyInChunks(t *testing.T) {
	payload := strings.Repeat("x", 5*defaultChunkSize)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	got := execute(t, context.Background(), model.Request{Method: http.MethodGet, URL: srv.URL})

	require.NoError(t, got.err)
	require.Greater(t, got.chunks, 1, "a large payload renders progressively, not in one lump")
	require.Equal(t, payload, string(got.body))
	require.Equal(t, int64(len(payload)), got.done.Size)
}

func TestExecuteSizesTheBodyFromContentLength(t *testing.T) {
	// A size that is not a power of two would land in a larger allocation class if the
	// buffer had been grown into, so an exact capacity is how the hint reaching streamBody
	// is observable from out here.
	payload := strings.Repeat("z", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	got := execute(t, context.Background(), model.Request{Method: http.MethodGet, URL: srv.URL})

	require.NoError(t, got.err)
	require.Equal(t, payload, string(got.done.Body))
	require.Equal(t, len(payload), cap(got.done.Body))
}

func TestExecuteGivesTheDoneChunkItsOwnHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Rate-Limit", "100")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	ch, err := New().Execute(context.Background(), model.Request{Method: http.MethodGet, URL: srv.URL})
	require.NoError(t, err)

	var meta, done *model.Response
	for chunk := range ch {
		switch {
		case chunk.Meta != nil:
			meta = chunk.Meta
			// A consumer is free to do what it likes with a chunk it has been handed.
			meta.Headers[0].Value = "rewritten by the consumer"
		case chunk.Done != nil:
			done = chunk.Done
		}
	}

	require.NotNil(t, meta)
	require.NotNil(t, done)
	require.NotEqual(t, "rewritten by the consumer", done.Headers[0].Value,
		"the two chunks must not share a backing array")
}

func TestExecuteDescribesFailuresInTheTrace(t *testing.T) {
	srv := chainServer(t)

	got := execute(t, context.Background(), model.Request{
		Method:   http.MethodGet,
		URL:      srv.URL + "/hop/5",
		Settings: model.RequestSettings{FollowRedirects: true, MaxRedirects: 2},
	})

	require.Equal(t, KindTooManyRedirects, KindOf(got.err))
	require.NotNil(t, got.trace, "a failure is still worth describing")
	require.Positive(t, got.trace.Duration, "how long it took before giving up")
	require.Len(t, got.trace.Redirects, 2, "and how far it got")
	require.Empty(t, got.trace.Proto, "no response arrived, so nothing was negotiated")
}

func TestExecuteReportsTrailers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "X-Checksum")
		_, _ = w.Write([]byte("payload"))
		w.Header().Set("X-Checksum", "abc123") // written after the body
	}))
	defer srv.Close()

	got := execute(t, context.Background(), model.Request{Method: http.MethodGet, URL: srv.URL})

	require.NoError(t, got.err)
	require.Equal(t, "payload", string(got.done.Body))
	require.Equal(t, []model.Header{{Key: "X-Checksum", Value: "abc123", Enabled: true}}, got.trace.Trailers)
	require.Equal(t, got.done.Duration, got.trace.Duration, "one clock reading, reported once")
}

func TestExecuteIdentifiesQuiverToTheServer(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.UserAgent()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	out := execute(t, context.Background(), model.Request{Method: http.MethodGet, URL: srv.URL})
	require.NoError(t, out.err)
	require.Equal(t, userAgent, got)
	require.NotContains(t, got, "Go-http-client")
}

func TestExecuteReportsAServerErrorAsAResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	got := execute(t, context.Background(), model.Request{Method: http.MethodGet, URL: srv.URL})

	// A 500 is a response, not an engine failure: only the transport can fail a request.
	require.NoError(t, got.err)
	require.Equal(t, http.StatusInternalServerError, got.done.Status)
	require.Equal(t, `{"error":"boom"}`, string(got.done.Body))
}
