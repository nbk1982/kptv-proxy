package parser

import (
	"context"
	"io"
	"sync/atomic"
)

// byteCounterKey is the context key under which an import run hands its
// parsers a counter for downloaded bytes.
type byteCounterKey struct{}

// WithByteCounter returns a context whose parsers add every byte they download
// to counter. The import loop reads it to report progress while a large
// catalog is still on the wire, so a multi-minute download does not look like
// a hang in the log.
func WithByteCounter(ctx context.Context, counter *atomic.Int64) context.Context {
	return context.WithValue(ctx, byteCounterKey{}, counter)
}

// countingBody wraps a response body so its bytes are added to the counter
// carried by ctx. Without a counter the body is returned unchanged.
func countingBody(ctx context.Context, body io.Reader) io.Reader {
	counter, ok := ctx.Value(byteCounterKey{}).(*atomic.Int64)
	if !ok || counter == nil {
		return body
	}
	return &countingReader{r: body, n: counter}
}

// countingReader adds every byte read through it to n.
type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n.Add(int64(n))
	}
	return n, err
}
