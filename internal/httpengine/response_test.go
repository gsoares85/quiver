package httpengine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gsoares85/quiver/internal/model"
	"github.com/stretchr/testify/require"
)

// drain collects every chunk sent to out while fn runs.
func drain(t *testing.T, fn func(out chan<- Chunk)) []Chunk {
	t.Helper()
	out := make(chan Chunk, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(out)
		close(out)
	}()

	var chunks []Chunk
	for chunk := range out {
		chunks = append(chunks, chunk)
	}
	<-done
	return chunks
}

func TestStatusText(t *testing.T) {
	tests := []struct {
		name string
		resp *http.Response
		want string
	}{
		{
			name: "reason phrase from the server",
			resp: &http.Response{StatusCode: 200, Status: "200 OK"},
			want: "OK",
		},
		{
			name: "a custom reason phrase is kept",
			resp: &http.Response{StatusCode: 429, Status: "429 Slow Down Please"},
			want: "Slow Down Please",
		},
		{
			// HTTP/2 carries no reason phrase.
			name: "no reason phrase falls back to the registered text",
			resp: &http.Response{StatusCode: 404, Status: "404"},
			want: "Not Found",
		},
		{
			name: "an unknown status without a phrase reads as empty",
			resp: &http.Response{StatusCode: 799, Status: "799"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, statusText(tt.resp))
		})
	}
}

func TestHeadersFromIsDeterministic(t *testing.T) {
	header := http.Header{
		"X-Rate-Limit": []string{"100"},
		"Set-Cookie":   []string{"a=1", "b=2"},
		"Content-Type": []string{"application/json"},
	}

	require.Equal(t, []model.Header{
		{Key: "Content-Type", Value: "application/json", Enabled: true},
		{Key: "Set-Cookie", Value: "a=1", Enabled: true},
		{Key: "Set-Cookie", Value: "b=2", Enabled: true},
		{Key: "X-Rate-Limit", Value: "100", Enabled: true},
	}, headersFrom(header), "keys sort, repeated values keep the order the server sent")
}

func TestMetaFrom(t *testing.T) {
	meta := metaFrom(&http.Response{
		StatusCode: http.StatusCreated,
		Status:     "201 Created",
		Header:     http.Header{"Location": []string{"/v1/users/1"}},
	})

	require.Equal(t, http.StatusCreated, meta.Status)
	require.Equal(t, "Created", meta.StatusText)
	require.Equal(t, []model.Header{{Key: "Location", Value: "/v1/users/1", Enabled: true}}, meta.Headers)
	require.Empty(t, meta.Body, "the body is still streaming when meta is emitted")
	require.Zero(t, meta.Size)
}

func TestStreamBodyChunksInOrder(t *testing.T) {
	payload := strings.Repeat("ab", defaultChunkSize) // spans more than one read

	var (
		assembled []byte
		size      int64
		err       error
	)
	chunks := drain(t, func(out chan<- Chunk) {
		assembled, size, err = streamBody(context.Background(), strings.NewReader(payload), int64(len(payload)), out)
	})

	require.NoError(t, err)
	require.Equal(t, payload, string(assembled))
	require.Equal(t, int64(len(payload)), size)
	require.Greater(t, len(chunks), 1, "a payload larger than the buffer arrives in pieces")

	var streamed []byte
	for _, chunk := range chunks {
		require.Nil(t, chunk.Meta)
		require.Nil(t, chunk.Done)
		require.NoError(t, chunk.Err)
		streamed = append(streamed, chunk.Body...)
	}
	require.Equal(t, payload, string(streamed))
}

func TestStreamBodyGivesEachChunkItsOwnSlice(t *testing.T) {
	// The read buffer is reused, so a consumer that keeps a chunk must not see it change
	// when the next one is read.
	payload := strings.Repeat("a", defaultChunkSize) + strings.Repeat("b", 16)

	var assembled []byte
	chunks := drain(t, func(out chan<- Chunk) {
		assembled, _, _ = streamBody(context.Background(), strings.NewReader(payload), int64(len(payload)), out)
	})
	require.Len(t, chunks, 2)

	first := chunks[0].Body
	require.Equal(t, byte('a'), first[0], "the first chunk still holds its own bytes")

	// Mutating a delivered chunk must not reach back into the assembled response.
	first[0] = 'z'
	require.Equal(t, byte('a'), assembled[0])
}

func TestStreamBodyStopsWhenTheCallerGoesAway(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := make(chan Chunk) // unbuffered: the send can only complete if someone reads
	_, _, err := streamBody(ctx, strings.NewReader("payload"), -1, out)
	require.ErrorIs(t, err, context.Canceled)
}

func TestStreamBodySurfacesReadFailures(t *testing.T) {
	var (
		assembled []byte
		size      int64
		err       error
	)
	chunks := drain(t, func(out chan<- Chunk) {
		reader := io.MultiReader(strings.NewReader("partial"), errReader{errors.New("connection reset")})
		assembled, size, err = streamBody(context.Background(), reader, -1, out)
	})

	require.ErrorContains(t, err, "connection reset")
	require.Equal(t, "partial", string(assembled), "what did arrive is kept")
	require.Equal(t, int64(7), size)
	require.Len(t, chunks, 1, "the bytes that made it were still delivered")
}

func TestStreamBodyWithNoContent(t *testing.T) {
	var (
		assembled []byte
		size      int64
		err       error
	)
	chunks := drain(t, func(out chan<- Chunk) {
		assembled, size, err = streamBody(context.Background(), http.NoBody, -1, out)
	})

	require.NoError(t, err)
	require.Empty(t, assembled)
	require.Zero(t, size)
	require.Empty(t, chunks, "an empty body produces no chunk at all")
}

func TestStreamBodySizesTheBufferFromTheHint(t *testing.T) {
	payload := strings.Repeat("x", 5000)

	var assembled []byte
	drain(t, func(out chan<- Chunk) {
		assembled, _, _ = streamBody(context.Background(), strings.NewReader(payload), int64(len(payload)), out)
	})

	require.Equal(t, payload, string(assembled))
	require.Equal(t, len(payload), cap(assembled),
		"a known Content-Length is allocated once instead of grown into")
}

func TestStreamBodyCapsWhatAContentLengthMayClaim(t *testing.T) {
	// Content-Length is server-controlled. Sizing a buffer against an absurd claim would
	// hand any endpoint an out-of-memory button.
	var assembled []byte
	drain(t, func(out chan<- Chunk) {
		assembled, _, _ = streamBody(context.Background(), strings.NewReader("tiny"), 1<<40, out)
	})

	require.Equal(t, "tiny", string(assembled))
	require.LessOrEqual(t, cap(assembled), maxPrealloc, "the claim is a hint, not an allocation order")
}

func TestStreamBodyOutgrowsAnUnderstatedHint(t *testing.T) {
	// A server that understates Content-Length must not cost us bytes: the hint only sizes
	// the buffer, it never bounds what is read.
	payload := strings.Repeat("y", 40_000)

	var (
		assembled []byte
		size      int64
		err       error
	)
	drain(t, func(out chan<- Chunk) {
		assembled, size, err = streamBody(context.Background(), strings.NewReader(payload), 10, out)
	})

	require.NoError(t, err)
	require.Equal(t, payload, string(assembled))
	require.Equal(t, int64(len(payload)), size)
}

// errReader fails on the first read.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }
