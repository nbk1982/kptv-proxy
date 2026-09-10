package admin

import (
	"strings"

	"kptv-proxy/work/filter"
	"kptv-proxy/work/types"
)

// previewListPageSize is the largest page of verdicts a preview returns, and
// the default when the request names none. A catalog can run to hundreds of
// thousands of entries; a thousand rows is what a browser lists comfortably.
const previewListPageSize = 1000

// previewListRequest selects the page of per-stream verdicts a preview returns
// alongside its report: which outcome, an optional text search over name and
// group, an optional content type, and the page.
type previewListRequest struct {
	Verdict string `json:"verdict"` // kept, dropped or all; kept when empty
	Query   string `json:"q"`
	Type    string `json:"type"` // live, vod, series or "" for any
	Page    int    `json:"page"`
	Size    int    `json:"size"`
}

// previewListPage is one page of verdicts. Kept and Dropped count every
// stream the query and type match, whichever verdict is listed, so the UI can
// label its verdict switch with the counts of the current search.
type previewListPage struct {
	Total   int              `json:"total"` // streams matching query, type and verdict
	Kept    int              `json:"kept"`
	Dropped int              `json:"dropped"`
	Page    int              `json:"page"`
	Size    int              `json:"size"`
	Items   []filter.Verdict `json:"items"`
}

// pagePreviewVerdicts narrows the verdicts to the request and returns the page
// it asks for. Pages past the end come back clamped to the last one rather
// than empty, so a search that shrinks the list never strands the client.
func pagePreviewVerdicts(verdicts []filter.Verdict, req previewListRequest) previewListPage {
	page := previewListPage{Items: []filter.Verdict{}}

	query := strings.ToLower(strings.TrimSpace(req.Query))
	wantType := types.ContentType(strings.ToLower(strings.TrimSpace(req.Type)))
	wantKept, wantDropped := true, false
	switch strings.ToLower(req.Verdict) {
	case "dropped":
		wantKept, wantDropped = false, true
	case "all":
		wantDropped = true
	}

	matches := make([]filter.Verdict, 0, len(verdicts))
	for _, v := range verdicts {
		if wantType != "" && v.Type != wantType {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(v.Name), query) && !strings.Contains(strings.ToLower(v.Group), query) {
			continue
		}
		if v.Kept {
			page.Kept++
		} else {
			page.Dropped++
		}
		if (v.Kept && wantKept) || (!v.Kept && wantDropped) {
			matches = append(matches, v)
		}
	}
	page.Total = len(matches)

	page.Size = req.Size
	if page.Size < 1 || page.Size > previewListPageSize {
		page.Size = previewListPageSize
	}
	page.Page = req.Page
	if page.Page < 1 {
		page.Page = 1
	}
	if last := (page.Total + page.Size - 1) / page.Size; last > 0 && page.Page > last {
		page.Page = last
	}

	start := (page.Page - 1) * page.Size
	if start < page.Total {
		end := start + page.Size
		if end > page.Total {
			end = page.Total
		}
		page.Items = matches[start:end]
	}
	return page
}
