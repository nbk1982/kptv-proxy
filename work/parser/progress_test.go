package parser

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCountingBodyAddsToContextCounter(t *testing.T) {
	var got atomic.Int64
	ctx := WithByteCounter(context.Background(), &got)

	payload := strings.Repeat("#EXTINF:-1,x\nhttp://a/b\n", 500)
	body := countingBody(ctx, strings.NewReader(payload))

	if _, err := io.Copy(io.Discard, body); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if got.Load() != int64(len(payload)) {
		t.Fatalf("counted %d bytes, want %d", got.Load(), len(payload))
	}
}

func TestCountingBodyWithoutCounterIsPassthrough(t *testing.T) {
	r := strings.NewReader("abc")
	if got := countingBody(context.Background(), r); got != io.Reader(r) {
		t.Fatalf("expected the original reader back when no counter is set")
	}
}
