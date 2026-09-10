package admin

import (
	"fmt"
	"testing"

	"kptv-proxy/work/filter"
	"kptv-proxy/work/types"
)

func sampleVerdicts() []filter.Verdict {
	out := make([]filter.Verdict, 0, 2500)
	for i := 0; i < 2500; i++ {
		v := filter.Verdict{
			Name:  fmt.Sprintf("Channel %04d", i),
			Group: "Sports",
			Type:  types.ContentTypeLive,
			Kept:  i%5 != 0, // every fifth stream is dropped
			Stage: filter.StageDefault,
		}
		if i%7 == 0 {
			v.Group = "Movies"
			v.Type = types.ContentTypeVOD
		}
		out = append(out, v)
	}
	return out
}

func TestPagePreviewVerdictsDefaultsToKeptFirstThousand(t *testing.T) {
	page := pagePreviewVerdicts(sampleVerdicts(), previewListRequest{})
	if page.Size != previewListPageSize || page.Page != 1 {
		t.Fatalf("defaults: size=%d page=%d", page.Size, page.Page)
	}
	if page.Kept != 2000 || page.Dropped != 500 || page.Total != 2000 {
		t.Fatalf("counts: kept=%d dropped=%d total=%d", page.Kept, page.Dropped, page.Total)
	}
	if len(page.Items) != 1000 || !page.Items[0].Kept {
		t.Fatalf("first page should hold 1000 kept items, got %d", len(page.Items))
	}
}

func TestPagePreviewVerdictsSearchTypeAndVerdict(t *testing.T) {
	// search matches name or group case-insensitively, before the verdict split
	page := pagePreviewVerdicts(sampleVerdicts(), previewListRequest{Query: "MOVIES", Verdict: "all"})
	if page.Total != 358 || page.Kept+page.Dropped != page.Total {
		t.Fatalf("search: total=%d kept=%d dropped=%d", page.Total, page.Kept, page.Dropped)
	}

	dropped := pagePreviewVerdicts(sampleVerdicts(), previewListRequest{Verdict: "dropped", Type: "live"})
	for _, v := range dropped.Items {
		if v.Kept || v.Type != types.ContentTypeLive {
			t.Fatalf("dropped/live page leaked %+v", v)
		}
	}
	if dropped.Total != len(dropped.Items) || dropped.Total == 0 {
		t.Fatalf("dropped/live total=%d items=%d", dropped.Total, len(dropped.Items))
	}
}

func TestPagePreviewVerdictsClampsPageAndSize(t *testing.T) {
	// a page past the end lands on the last page instead of coming back empty
	page := pagePreviewVerdicts(sampleVerdicts(), previewListRequest{Page: 99, Size: 300})
	if page.Page != 7 || len(page.Items) != 200 {
		t.Fatalf("clamp: page=%d items=%d", page.Page, len(page.Items))
	}

	// an oversized page request falls back to the maximum
	page = pagePreviewVerdicts(sampleVerdicts(), previewListRequest{Size: 5000})
	if page.Size != previewListPageSize {
		t.Fatalf("size not clamped: %d", page.Size)
	}

	// no matches: an empty, non-nil list on page 1
	page = pagePreviewVerdicts(sampleVerdicts(), previewListRequest{Query: "nothing like this"})
	if page.Total != 0 || page.Page != 1 || page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("empty: %+v", page)
	}
}
