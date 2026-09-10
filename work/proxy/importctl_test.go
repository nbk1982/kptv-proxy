package proxy

import (
	"context"
	"testing"
	"time"

	"kptv-proxy/work/constants"
	"kptv-proxy/work/types"
)

// stamped builds a stream carrying the type an importer would have stamped.
func stamped(name string, contentType types.ContentType) *types.Stream {
	return &types.Stream{Name: name, ContentType: contentType}
}

func newSlot() *previewSlot {
	return &previewSlot{sem: make(chan struct{}, 1)}
}

func TestPreviewSlotRestoresImporterStamps(t *testing.T) {
	streams := []*types.Stream{stamped("Live one", types.ContentTypeLive), stamped("Movie", types.ContentTypeVOD)}
	slot := newSlot()
	slot.put("key", streams)

	// a previous pass decided something else and wrote it onto the stream;
	// the shared resolver treats a stamp as authoritative, so the next pass
	// has to start from what the parser actually said
	streams[0].ContentType = types.ContentTypeSeries
	streams[1].ContentType = types.ContentTypeSeries

	got, ok := slot.take("key")
	if !ok || len(got) != 2 {
		t.Fatalf("fresh catalog should come back, ok=%v len=%d", ok, len(got))
	}
	if got[0].ContentType != types.ContentTypeLive || got[1].ContentType != types.ContentTypeVOD {
		t.Fatalf("stamps not restored: %q, %q", got[0].ContentType, got[1].ContentType)
	}
}

func TestPreviewSlotMissesOtherKeysAndReleasesStaleCatalogs(t *testing.T) {
	slot := newSlot()
	slot.put("key", []*types.Stream{stamped("x", types.ContentTypeLive)})

	if _, ok := slot.take("other"); ok {
		t.Fatal("another source's catalog must not be reused")
	}
	if slot.streams != nil {
		t.Fatal("a catalog for another source must be released, not held")
	}

	slot.put("key", []*types.Stream{stamped("x", types.ContentTypeLive)})
	slot.fetchedAt = time.Now().Add(-constants.Internal.PreviewCacheTTL - time.Second)
	if _, ok := slot.take("key"); ok {
		t.Fatal("a stale catalog must not be reused")
	}
	if slot.streams != nil {
		t.Fatal("a stale catalog must be released so its memory is not held until the next preview")
	}

	slot.put("key", []*types.Stream{stamped("x", types.ContentTypeLive)})
	slot.drop("other")
	if slot.streams == nil {
		t.Fatal("drop must only affect the named source")
	}
	slot.drop("key")
	if slot.streams != nil {
		t.Fatal("drop must release the named source's catalog")
	}
}

func TestPreviewSlotSerializesAndHonoursContext(t *testing.T) {
	slot := newSlot()
	if err := slot.acquire(context.Background()); err != nil {
		t.Fatalf("first acquire should succeed: %v", err)
	}

	// a second preview waits its turn, and gives up when its request ends
	// rather than blocking on a mutex it cannot cancel
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := slot.acquire(ctx); err == nil {
		t.Fatal("a second acquire must not succeed while the slot is held")
	} else if err != context.DeadlineExceeded {
		t.Fatalf("want the context error, got %v", err)
	}

	slot.release()
	if err := slot.acquire(context.Background()); err != nil {
		t.Fatalf("the slot must be free after release: %v", err)
	}
	slot.release()
}
