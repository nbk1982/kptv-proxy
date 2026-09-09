package filter

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"strings"
	"sync"

	"github.com/grafana/regexp"
)

// groupAttributeKeys are the M3U attributes that carry a stream's group label,
// in the same precedence order utils.ContentTypeOfStream uses.
var groupAttributeKeys = []string{"group-title", "tvg-group"}

// CompiledFilter holds compiled regex patterns for a source. The Category
// patterns decide which content type a stream is; the Include/Exclude patterns
// decide whether a stream of that type survives the import.
type CompiledFilter struct {
	LiveCategory   *regexp.Regexp
	VODCategory    *regexp.Regexp
	SeriesCategory *regexp.Regexp
	LiveInclude    *regexp.Regexp
	LiveExclude    *regexp.Regexp
	SeriesInclude  *regexp.Regexp
	SeriesExclude  *regexp.Regexp
	VODInclude     *regexp.Regexp
	VODExclude     *regexp.Regexp
	signature      string
}

// FilterManager manages compiled filters for sources
type FilterManager struct {
	filters map[string]*CompiledFilter
	mu      sync.RWMutex
}

// NewFilterManager creates a new filter manager
func NewFilterManager() *FilterManager {
	return &FilterManager{
		filters: make(map[string]*CompiledFilter),
	}
}

// compilePattern compiles one configured pattern. An empty pattern means "no
// filter of this kind"; an invalid one is logged and also treated as no filter,
// so a typo can never take a whole source offline.
func compilePattern(field, pattern string) *regexp.Regexp {
	if pattern == "" {
		return nil
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		logger.Error("{filter - GetOrCreateFilter} failed to compile %s '%s': %v\n", field, pattern, err)
		return nil
	}
	logger.Debug("{filter - GetOrCreateFilter} Compiled %s: '%s'\n", field, pattern)
	return compiled
}

// GetOrCreateFilter gets or creates a compiled filter for a source
func (fm *FilterManager) GetOrCreateFilter(source *config.SourceConfig) *CompiledFilter {

	// et a filter lock
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Use source URL as key since it's unique
	key := source.URL

	// the cache is keyed on URL, so a regex edit alone would otherwise keep serving the stale filter
	signature := strings.Join([]string{
		source.LiveCategoryRegex,
		source.VODCategoryRegex,
		source.SeriesCategoryRegex,
		source.LiveIncludeRegex,
		source.LiveExcludeRegex,
		source.SeriesIncludeRegex,
		source.SeriesExcludeRegex,
		source.VODIncludeRegex,
		source.VODExcludeRegex,
	}, "\x00")

	// if it already exists and nothing changed
	if filter, exists := fm.filters[key]; exists {
		if filter.signature == signature {
			return filter
		}
		delete(fm.filters, key)
		logger.Debug("{filter - GetOrCreateFilter} filter patterns changed, recompiling for: %s", source.Name)
	}

	// setup the compiled filter (an invalid pattern is logged and skipped)
	filter := &CompiledFilter{
		signature:      signature,
		LiveCategory:   compilePattern("LiveCategoryRegex", source.LiveCategoryRegex),
		VODCategory:    compilePattern("VODCategoryRegex", source.VODCategoryRegex),
		SeriesCategory: compilePattern("SeriesCategoryRegex", source.SeriesCategoryRegex),
		LiveInclude:    compilePattern("LiveIncludeRegex", source.LiveIncludeRegex),
		LiveExclude:    compilePattern("LiveExcludeRegex", source.LiveExcludeRegex),
		SeriesInclude:  compilePattern("SeriesIncludeRegex", source.SeriesIncludeRegex),
		SeriesExclude:  compilePattern("SeriesExcludeRegex", source.SeriesExcludeRegex),
		VODInclude:     compilePattern("VODIncludeRegex", source.VODIncludeRegex),
		VODExclude:     compilePattern("VODExcludeRegex", source.VODExcludeRegex),
	}

	fm.filters[key] = filter
	logger.Debug("{filter - GetOrCreateFilter} setup the compiled filter")
	// return it
	return filter
}

// ClearFilters clears all compiled filters
func (fm *FilterManager) ClearFilters() {
	logger.Debug("{filter - ClearFilters} clear the filter map")
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fm.filters = make(map[string]*CompiledFilter)
}

// RemoveFilter removes a specific filter
func (fm *FilterManager) RemoveFilter(sourceURL string) {
	logger.Debug("{filter - RemoveFilter} remove the filter for %v", sourceURL)
	fm.mu.Lock()
	defer fm.mu.Unlock()
	delete(fm.filters, sourceURL)
}

// matchSubjects returns every string a source pattern is tested against: the
// stream name, its group labels, and its URL. They are kept as separate strings
// rather than concatenated so an anchored pattern like `^hbo` still means
// "this field starts with", and all are lowercased and trimmed so patterns
// written for the old name-only behaviour keep matching unchanged.
func matchSubjects(stream *types.Stream) []string {
	subjects := make([]string, 0, len(groupAttributeKeys)+2)
	add := func(value string) {
		if value = strings.TrimSpace(strings.ToLower(value)); value != "" {
			subjects = append(subjects, value)
		}
	}

	add(stream.Name)
	for _, key := range groupAttributeKeys {
		add(stream.Attributes[key])
	}
	add(stream.URL)

	return subjects
}

// matchesAny reports whether the pattern matches at least one subject.
func matchesAny(pattern *regexp.Regexp, subjects []string) bool {
	for _, subject := range subjects {
		if pattern.MatchString(subject) {
			return true
		}
	}
	return false
}

// resolveContentType applies the per-source category patterns, falling back to
// the shared resolver when none of them match or none are configured.
//
// A matching category pattern deliberately outranks the type the importer
// stamped on the stream: every importer already stamps an explicit type, so a
// pattern that only filled in the gaps would never fire. Series is tested
// first and live last, from most to least specific — series entries routinely
// also carry "movie" or "vod" in their URL path, and live is the type every
// unrecognised entry already falls back to.
func resolveContentType(stream *types.Stream, filter *CompiledFilter, subjects []string) types.ContentType {
	switch {
	case filter.SeriesCategory != nil && matchesAny(filter.SeriesCategory, subjects):
		return types.ContentTypeSeries
	case filter.VODCategory != nil && matchesAny(filter.VODCategory, subjects):
		return types.ContentTypeVOD
	case filter.LiveCategory != nil && matchesAny(filter.LiveCategory, subjects):
		return types.ContentTypeLive
	}
	return utils.ContentTypeOfStream(stream)
}

// FilterStreams classifies each stream against the source's category patterns
// and then applies that type's include/exclude patterns.
func FilterStreams(streams []*types.Stream, source *config.SourceConfig, filterManager *FilterManager) []*types.Stream {

	// Debug logging to see if filtering is being called
	if len(streams) > 0 && streams[0].Source != nil && streams[0].Source.Name != "" {
		logger.Debug("{filter - FilterStreams} Source: %s, Streams: %d, Category: live=%s vod=%s series=%s, Include: live=%s series=%s vod=%s, Exclude: live=%s series=%s vod=%s\n",
			source.Name, len(streams),
			source.LiveCategoryRegex, source.VODCategoryRegex, source.SeriesCategoryRegex,
			source.LiveIncludeRegex, source.SeriesIncludeRegex, source.VODIncludeRegex,
			source.LiveExcludeRegex, source.SeriesExcludeRegex, source.VODExcludeRegex)
	}

	// every pattern must be checked here — a source carrying only category
	// patterns still needs the loop below to run in order to be reclassified
	if source.LiveCategoryRegex == "" && source.VODCategoryRegex == "" && source.SeriesCategoryRegex == "" &&
		source.LiveIncludeRegex == "" && source.LiveExcludeRegex == "" &&
		source.SeriesIncludeRegex == "" && source.SeriesExcludeRegex == "" &&
		source.VODIncludeRegex == "" && source.VODExcludeRegex == "" {
		logger.Debug("{filter - FilterStreams} No filters configured for source %s, returning %d streams unchanged\n", source.Name, len(streams))
		// return the streams
		return streams
	}
	logger.Debug("{filter - FilterStreams} Applying filters to %d streams from source %s\n", len(streams), source.Name)

	filter := filterManager.GetOrCreateFilter(source)
	filtered := make([]*types.Stream, 0, len(streams))

	for _, stream := range streams {
		subjects := matchSubjects(stream)
		contentType := resolveContentType(stream, filter, subjects)

		// persist the verdict on the stream. utils.ContentTypeOfStream returns an
		// explicit type verbatim, so every downstream consumer — playlist output,
		// XC catalogs, playback routing — now serves the stream as the same type
		// it was filtered as, with no second classification to disagree with.
		stream.ContentType = contentType

		shouldInclude := shouldIncludeStream(stream, filter, contentType, subjects)
		logger.Debug("{filter - FilterStreams} Stream: %s, Type: %s, Include: %v\n", stream.Name, contentType, shouldInclude)

		if shouldInclude {
			filtered = append(filtered, stream)
		}
	}
	logger.Debug("{filter - FilterStreams} Filtered %d -> %d streams for source %s\n", len(streams), len(filtered), source.Name)

	// return the filtered streams
	return filtered
}

// shouldIncludeStream determines if a stream should be included based on the
// include/exclude patterns for the content type it was classified as.
func shouldIncludeStream(stream *types.Stream, filter *CompiledFilter, contentType types.ContentType, subjects []string) bool {
	originalName := stream.Name
	logger.Debug("{filter - shouldIncludeStream} Evaluating stream: '%s', subjects: %v, content type: %s\n", originalName, subjects, contentType)

	// Check include filters first - if any exist, stream must match at least one
	var hasIncludeFilters bool
	var matchesInclude bool

	switch contentType {
	case types.ContentTypeLive:
		if filter.LiveInclude != nil {
			hasIncludeFilters = true
			matchesInclude = matchesAny(filter.LiveInclude, subjects)
			logger.Debug("{filter - shouldIncludeStream} Live include pattern exists, matches: %v (tested against: %v)\n", matchesInclude, subjects)

		}
	case types.ContentTypeSeries:
		if filter.SeriesInclude != nil {
			hasIncludeFilters = true
			matchesInclude = matchesAny(filter.SeriesInclude, subjects)
			logger.Debug("{filter - shouldIncludeStream} Series include pattern exists, matches: %v\n", matchesInclude)

		}
	case types.ContentTypeVOD:
		if filter.VODInclude != nil {
			hasIncludeFilters = true
			matchesInclude = matchesAny(filter.VODInclude, subjects)
			logger.Debug("{filter - shouldIncludeStream} VOD include pattern exists, matches: %v\n", matchesInclude)

		}
	}

	logger.Debug("{filter - shouldIncludeStream} hasIncludeFilters: %v, matchesInclude: %v\n", hasIncludeFilters, matchesInclude)

	// If include filters exist but stream doesn't match any, exclude it
	if hasIncludeFilters && !matchesInclude {
		logger.Debug("{filter - shouldIncludeStream} EXCLUDED by include filters: '%s'\n", originalName)
		return false
	}

	// Then check exclude filters
	switch contentType {
	case types.ContentTypeLive:
		if filter.LiveExclude != nil {
			if matchesAny(filter.LiveExclude, subjects) {
				logger.Debug("{filter - shouldIncludeStream} EXCLUDED by live exclude filter: '%s'\n", originalName)
				return false
			}
			logger.Debug("{filter - shouldIncludeStream} Live exclude pattern exists but didn't match: %v\n", subjects)
		}
	case types.ContentTypeSeries:
		if filter.SeriesExclude != nil {
			if matchesAny(filter.SeriesExclude, subjects) {
				logger.Debug("{filter - shouldIncludeStream} EXCLUDED by series exclude filter: '%s'\n", originalName)
				return false
			}
		}
	case types.ContentTypeVOD:
		if filter.VODExclude != nil {
			if matchesAny(filter.VODExclude, subjects) {
				logger.Debug("{filter - shouldIncludeStream} EXCLUDED by VOD exclude filter: '%s'\n", originalName)
				return false
			}
		}
	}

	logger.Debug("{filter - shouldIncludeStream} INCLUDED: '%s'\n", originalName)
	return true
}
