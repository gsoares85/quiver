package httpengine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gsoares85/quiver/internal/model"
)

const (
	// defaultChunkSize is how much body is read per streamed chunk. It is large enough to
	// keep syscalls cheap and small enough that a progress bar still moves.
	defaultChunkSize = 32 * 1024

	// opReadBody labels failures raised while streaming a response body.
	opReadBody = "read response body"
)

// metaFrom captures the part of a response that is known before the body arrives.
func metaFrom(resp *http.Response) *model.Response {
	return &model.Response{
		Status:     resp.StatusCode,
		StatusText: statusText(resp),
		Headers:    headersFrom(resp.Header),
	}
}

// statusText is the reason phrase the server sent, falling back to the registered text
// for the status code. HTTP/2 carries no reason phrase at all, so the fallback is what a
// front-end shows there.
func statusText(resp *http.Response) string {
	text := strings.TrimSpace(strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)))
	if text != "" {
		return text
	}
	return http.StatusText(resp.StatusCode)
}

// headersFrom flattens response headers into the model's ordered list. Keys are sorted
// and repeated values kept in the order the server sent them, so the same response always
// produces the same data — which is what keeps a saved example diff-stable.
func headersFrom(header http.Header) []model.Header {
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	out := make([]model.Header, 0, len(keys))
	for _, key := range keys {
		for _, value := range header[key] {
			out = append(out, model.Header{Key: key, Value: value, Enabled: true})
		}
	}
	return out
}

// streamBody forwards the response body to out in chunks and returns the assembled body
// with the number of bytes delivered.
//
// Size counts what the caller receives, which is the decompressed payload whenever the
// transport handled the encoding. Each chunk is a fresh slice: the read buffer is reused,
// and handing the same array to a consumer twice would corrupt what it already has.
func streamBody(ctx context.Context, body io.Reader, out chan<- Chunk) ([]byte, int64, error) {
	var (
		assembled []byte
		size      int64
		buf       = make([]byte, defaultChunkSize)
	)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			assembled = append(assembled, chunk...)
			size += int64(n)
			if !send(ctx, out, Chunk{Body: chunk}) {
				return assembled, size, ctx.Err()
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return assembled, size, nil
			}
			return assembled, size, err
		}
	}
}
