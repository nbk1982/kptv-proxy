// work/proxy/importctl.go
package proxy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/db"
	"kptv-proxy/work/filter"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
)

// ErrImportRunning is returned by TriggerImport while a catalog import is in
// progress; the caller should report it rather than queue another run.
var ErrImportRunning = errors.New("an import is already running")

// SourceImportStatus is what the admin UI shows on a source card: whether the
// source is being fetched right now and how its last import went.
type SourceImportStatus struct {
	URL          string     `json:"url"`
	Name         string     `json:"name"`
	Running      bool       `json:"running"`
	LastImportAt *time.Time `json:"lastImportAt,omitempty"`
	DurationMs   int64      `json:"durationMs"`
	Total        int        `json:"total"`
	Kept         int        `json:"kept"`
	OK           bool       `json:"ok"`
	Error        string     `json:"error,omitempty"`
}

// ImportStatus is the proxy-wide view: one running flag plus a row per
// configured source.
type ImportStatus struct {
	Running   bool                 `json:"running"`
	StartedAt *time.Time           `json:"startedAt,omitempty"`
	Sources   []SourceImportStatus `json:"sources"`
}

// previewSlot holds the raw catalog of the source most recently previewed, so
// an operator adjusting rules gets each re-evaluation from memory instead of a
// fresh download. One slot bounds the memory to a single catalog; a serialized
// catalog with hundreds of thousands of entries is too large for the shared
// otter cache, which is why the import path re-fetches and this one must not.
type previewSlot struct {
	// sem serializes previews with a capacity of one, so a caller can wait for
	// its turn under its own request context instead of blocking on a mutex
	// that cannot be cancelled while another preview downloads a catalog.
	sem       chan struct{}
	key       string
	fetchedAt time.Time
	streams   []*types.Stream
	stamps    []types.ContentType // each stream's type as the parser stamped it
}

// acquire takes the preview slot, or gives up when the caller's context ends.
func (s *previewSlot) acquire(ctx context.Context) error {
	select {
	case s.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release hands the slot to the next caller.
func (s *previewSlot) release() {
	<-s.sem
}

// take returns the cached catalog for key while it is fresh, with every stream
// restored to its parser stamp. Apply overwrites ContentType, and the shared
// resolver treats an existing stamp as authoritative, so a verdict from the
// previous preview would otherwise leak into the next one. A catalog that is
// stale or for another source is released here rather than held until the next
// preview replaces it, since it can be hundreds of megabytes. Callers hold the
// slot.
func (s *previewSlot) take(key string) ([]*types.Stream, bool) {
	if s.key != key || time.Since(s.fetchedAt) > constants.Internal.PreviewCacheTTL {
		s.streams, s.stamps = nil, nil
		return nil, false
	}
	if s.streams == nil {
		return nil, false
	}
	for i, stream := range s.streams {
		stream.ContentType = s.stamps[i]
	}
	return s.streams, true
}

// put replaces the slot's catalog. Callers hold mu.
func (s *previewSlot) put(key string, streams []*types.Stream) {
	stamps := make([]types.ContentType, len(streams))
	for i, stream := range streams {
		stamps[i] = stream.ContentType
	}
	s.key, s.fetchedAt, s.streams, s.stamps = key, time.Now(), streams, stamps
}

// drop forgets the slot's catalog when it belongs to key. Callers hold the slot.
func (s *previewSlot) drop(key string) {
	if s.key == key {
		s.streams, s.stamps = nil, nil
	}
}

// releasePreviewCatalog frees the previewed catalog. Called once an import has
// committed, so the memory a preview held does not outlive the editing session
// that needed it.
func (sp *StreamProxy) releasePreviewCatalog() {
	select {
	case sp.preview.sem <- struct{}{}:
		sp.preview.streams, sp.preview.stamps = nil, nil
		<-sp.preview.sem
	default:
		// a preview is running; it will replace or release the catalog itself
	}
}

// FetchSourceStreams pulls a source's raw catalog through the parser that
// matches it: the XC API when credentials are set, M3U otherwise. The result
// is unfiltered and comes from the raw cache while the last fetch is fresh.
func (sp *StreamProxy) FetchSourceStreams(ctx context.Context, src *config.SourceConfig) []*types.Stream {
	rateLimiter := sp.getRateLimiterForSource(src)
	if parser.IsXCSource(src) {
		logger.Debug("{proxy/importctl - FetchSourceStreams} Parsing Xtreme Codes API source: %s", src.Name)
		return parser.ParseXtremeCodesAPI(ctx, sp.ImportClient, sp.Config, src, rateLimiter, sp.Cache)
	}
	logger.Debug("{proxy/importctl - FetchSourceStreams} Parsing M3U8 source: %s", src.Name)
	return parser.ParseM3U8(ctx, sp.ImportClient, sp.Config, src, rateLimiter, sp.Cache)
}

// TriggerImport starts a catalog import in the background so a saved filter
// applies without a restart. Sources whose raw catalog is still cached are not
// fetched again, so re-applying a rule costs one pass over data already in
// memory. With force, the named source (or every source when sourceURL is
// empty) is dropped from the raw cache first and downloaded afresh.
func (sp *StreamProxy) TriggerImport(sourceURL string, force bool) error {
	// the gate is claimed before the goroutine starts, so two clicks in the
	// same instant cannot both queue a full import, and a status poll issued
	// right after this returns already reports the run
	if !sp.importPending.CompareAndSwap(false, true) {
		return ErrImportRunning
	}
	if force {
		for i := range sp.Config.Sources {
			src := &sp.Config.Sources[i]
			if sourceURL == "" || src.URL == sourceURL {
				sp.Cache.InvalidateXCData(parser.RawCacheKey(src))
			}
		}
	}
	go func() {
		defer sp.importPending.Store(false)
		sp.ImportStreams()
	}()
	return nil
}

// ImportStatus reports whether an import is running and, for every configured
// source, whether it is being fetched and how its last import went.
func (sp *StreamProxy) ImportStatus() ImportStatus {
	status := ImportStatus{
		// pending covers the window between TriggerImport returning and the
		// import goroutine actually starting, plus a manual run waiting behind
		// the scheduled one
		Running: sp.importRunning.Load() || sp.importPending.Load(),
		Sources: make([]SourceImportStatus, 0, len(sp.Config.Sources)),
	}
	if sp.importRunning.Load() {
		startedAt := time.Unix(0, sp.importStartedAt.Load())
		status.StartedAt = &startedAt
	}

	last, err := db.GetSourceImports()
	if err != nil {
		logger.Warn("{proxy/importctl - ImportStatus} Failed to load import history: %v", err)
	}

	for i := range sp.Config.Sources {
		src := &sp.Config.Sources[i]
		row := SourceImportStatus{URL: src.URL, Name: src.Name}
		_, row.Running = sp.importingSources.Load(src.URL)
		if rec, ok := last[src.URL]; ok {
			importedAt := rec.ImportedAt
			row.LastImportAt = &importedAt
			row.DurationMs = rec.DurationMs
			row.Total = rec.Total
			row.Kept = rec.Kept
			row.OK = rec.OK
			row.Error = rec.Error
		}
		status.Sources = append(status.Sources, row)
	}
	return status
}

// PreviewSource fetches a source with a draft configuration and reports what
// its rules would keep, without touching the live catalog. A throwaway filter
// manager keeps the draft's compiled rules out of the import's cache. The
// returned cached flag says whether the raw catalog was already in memory, so
// the UI can tell a sub-second re-evaluation from a full download.
func (sp *StreamProxy) PreviewSource(ctx context.Context, src *config.SourceConfig, force bool) (report *filter.Report, cached bool, err error) {
	// previews are serialized end to end: the slot's streams are shared and
	// Apply rewrites their type stamps, so two passes must never interleave
	if err := sp.preview.acquire(ctx); err != nil {
		return nil, false, err
	}
	defer sp.preview.release()

	key := parser.RawCacheKey(src)
	if force {
		sp.Cache.InvalidateXCData(key)
		sp.preview.drop(key)
	}

	streams, cached := sp.preview.take(key)
	if !cached {
		cached = sp.Cache.HasXCData(key)
		streams = sp.FetchSourceStreams(ctx, src)
		if ctx.Err() != nil {
			return nil, cached, fmt.Errorf("fetching %s timed out", utils.LogURL(sp.Config, src.URL))
		}
		if len(streams) == 0 {
			return nil, cached, fmt.Errorf("%s returned no streams", utils.LogURL(sp.Config, src.URL))
		}
		sp.preview.put(key, streams)
	}

	_, report = filter.Apply(streams, src, filter.NewFilterManager())
	return report, cached, nil
}

// recordSourceImport persists the outcome of one source's import: the summary
// row the status endpoint reads and, on success, the group inventory the
// filter UI offers for selection. A failed fetch keeps the previous inventory,
// since the catalog it described is the one still being served.
//
// It runs per source, as soon as that source has been filtered, so the record
// describes what the source contributed rather than what the run as a whole
// committed: a later run that produces no channels at all is abandoned and
// leaves these rows in place. Persistence errors are logged and never fatal.
func (sp *StreamProxy) recordSourceImport(src *config.SourceConfig, report *filter.Report, started time.Time, importErr error) {
	rec := db.SourceImport{
		SourceURL:  src.URL,
		ImportedAt: time.Now(),
		DurationMs: time.Since(started).Milliseconds(),
		OK:         importErr == nil,
	}
	if importErr != nil {
		rec.Error = importErr.Error()
	}

	if report != nil {
		rec.Total = report.Total
		rec.Kept = report.Kept
		groups := make([]db.SourceGroup, 0, len(report.Groups))
		for _, g := range report.Groups {
			groups = append(groups, db.SourceGroup{
				SourceURL:   src.URL,
				Name:        g.Name,
				ContentType: string(g.ContentType),
				Total:       g.Total,
				Kept:        g.Kept,
				Samples:     g.Samples,
			})
		}
		if err := db.ReplaceSourceGroups(src.URL, groups); err != nil {
			logger.Warn("{proxy/importctl - recordSourceImport} Failed to store group inventory for %s: %v", src.Name, err)
		}
	}

	if err := db.UpsertSourceImport(rec); err != nil {
		logger.Warn("{proxy/importctl - recordSourceImport} Failed to store import record for %s: %v", src.Name, err)
	}
}
