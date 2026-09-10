package filter

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/grafana/regexp"
)

// groupAttributeKeys are the M3U attributes that carry a stream's group label,
// in the same precedence order utils.ContentTypeOfStream uses.
var groupAttributeKeys = []string{"group-title", "tvg-group"}

// CompiledFilter holds the compiled rules for one source. Every stream goes
// through the stages in this order, and each stage only ever narrows: the
// group filter, classification into a content type (group override, category
// patterns, importer heuristics), the content type gate, and finally that
// type's include/exclude patterns.
type CompiledFilter struct {
	Rules       []compiledRule                 // ordered include/exclude rules; the first match decides
	RuleDefault string                         // verdict for a stream no rule matched
	GroupMode   string                         // config.GroupFilterInclude, config.GroupFilterExclude, or "" when off
	GroupSet    map[string]struct{}            // config.GroupKey of every listed group
	GroupRegex  *regexp.Regexp                 // optional pattern on the group label, ORed with GroupSet
	ImportTypes map[types.ContentType]struct{} // content types that are imported; nil imports every type
	GroupTypes  map[string]types.ContentType   // config.GroupKey -> content type forced for that group

	LiveCategory   *regexp.Regexp
	VODCategory    *regexp.Regexp
	SeriesCategory *regexp.Regexp
	LiveInclude    *regexp.Regexp
	LiveExclude    *regexp.Regexp
	SeriesInclude  *regexp.Regexp
	SeriesExclude  *regexp.Regexp
	VODInclude     *regexp.Regexp
	VODExclude     *regexp.Regexp
	QualityTiers   []*regexp.Regexp // quality markers best first; nil when the quality stage is off
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

// ruleSet is the ordered rule list a source is filtered by once its profile
// has been resolved, with the origin of each rule kept for the preview.
type ruleSet struct {
	rules   []config.FilterRule
	origins []string
	def     string
}

// resolveRules returns the source's effective rules. A nil config means no
// profiles exist, so only the source's own rules apply.
func resolveRules(source *config.SourceConfig, cfg *config.Config) ruleSet {
	if cfg == nil {
		origins := make([]string, len(source.FilterRules))
		for i := range origins {
			origins[i] = "source"
		}
		def := source.FilterDefault
		if def == "" {
			def = config.FilterDefaultKeep
		}
		return ruleSet{rules: source.FilterRules, origins: origins, def: def}
	}
	rules, origins := cfg.EffectiveRules(source)
	return ruleSet{rules: rules, origins: origins, def: cfg.EffectiveDefault(source)}
}

// filterSignature serializes every rule of a source so a cached filter can be
// recognised as stale. The override map is sorted so two equal maps always
// produce the same string.
func filterSignature(source *config.SourceConfig, rules ruleSet) string {
	overrides := make([]string, 0, len(source.GroupTypeOverrides))
	for group, contentType := range source.GroupTypeOverrides {
		overrides = append(overrides, group+"="+contentType)
	}
	sort.Strings(overrides)

	ruleParts := make([]string, 0, len(rules.rules)+1)
	ruleParts = append(ruleParts, rules.def)
	for i, rule := range rules.rules {
		ruleParts = append(ruleParts, rules.origins[i]+"|"+rule.Field+"|"+rule.Action+"|"+rule.Pattern)
	}

	return strings.Join([]string{
		strings.Join(ruleParts, "\x02"),
		source.LiveCategoryRegex,
		source.VODCategoryRegex,
		source.SeriesCategoryRegex,
		source.LiveIncludeRegex,
		source.LiveExcludeRegex,
		source.SeriesIncludeRegex,
		source.SeriesExcludeRegex,
		source.VODIncludeRegex,
		source.VODExcludeRegex,
		source.GroupFilterMode,
		strings.Join(source.GroupFilterList, "\x01"),
		source.GroupFilterRegex,
		strings.Join(source.ImportTypes, "\x01"),
		strings.Join(overrides, "\x01"),
		strconv.FormatBool(source.QualityDedupe),
		strings.Join(source.QualityTiers, "\x01"),
	}, "\x00")
}

// GetOrCreateFilter gets or creates a compiled filter for a source. The config
// supplies the shared rule profiles a source may name; it may be nil when
// there are none.
func (fm *FilterManager) GetOrCreateFilter(source *config.SourceConfig, cfg *config.Config) *CompiledFilter {

	// et a filter lock
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Use source URL as key since it's unique
	key := source.URL

	// the cache is keyed on URL, so a rule edit alone would otherwise keep serving the stale filter
	rules := resolveRules(source, cfg)
	signature := filterSignature(source, rules)

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
		Rules:          compileRules(rules.rules, rules.origins),
		RuleDefault:    rules.def,
		GroupMode:      source.GroupFilterMode,
		GroupRegex:     compilePattern("GroupFilterRegex", source.GroupFilterRegex),
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

	if len(source.GroupFilterList) > 0 {
		filter.GroupSet = make(map[string]struct{}, len(source.GroupFilterList))
		for _, group := range source.GroupFilterList {
			filter.GroupSet[config.GroupKey(group)] = struct{}{}
		}
	}
	if len(source.ImportTypes) > 0 {
		filter.ImportTypes = make(map[types.ContentType]struct{}, len(source.ImportTypes))
		for _, contentType := range source.ImportTypes {
			filter.ImportTypes[types.ContentType(contentType)] = struct{}{}
		}
	}
	if len(source.GroupTypeOverrides) > 0 {
		filter.GroupTypes = make(map[string]types.ContentType, len(source.GroupTypeOverrides))
		// sorted so two spellings of one label always resolve the same way;
		// NormalizeFilters rejects conflicting pairs, but a config written
		// before it existed can still carry them
		for _, group := range slices.Sorted(maps.Keys(source.GroupTypeOverrides)) {
			filter.GroupTypes[config.GroupKey(group)] = types.ContentType(source.GroupTypeOverrides[group])
		}
	}
	if source.QualityDedupe {
		tiers := source.QualityTiers
		if len(tiers) == 0 {
			tiers = config.DefaultQualityTiers
		}
		for _, tier := range tiers {
			if re := compilePattern("QualityTiers", config.QualityTierPattern(tier)); re != nil {
				filter.QualityTiers = append(filter.QualityTiers, re)
			}
		}
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

// passesGroupFilter applies the group stage. A group is "matched" when it is
// listed or when the group pattern hits it; include mode keeps matched groups
// and exclude mode drops them. Streams without a group carry the empty label,
// which an operator can list like any other group.
func (f *CompiledFilter) passesGroupFilter(group, groupKey string) bool {
	if f.GroupMode == config.GroupFilterOff {
		return true
	}
	_, matched := f.GroupSet[groupKey]
	if !matched && f.GroupRegex != nil {
		matched = f.GroupRegex.MatchString(groupSubject(group))
	}
	if f.GroupMode == config.GroupFilterExclude {
		return !matched
	}
	return matched
}

// importsType applies the content type gate.
func (f *CompiledFilter) importsType(contentType types.ContentType) bool {
	if f.ImportTypes == nil {
		return true
	}
	_, ok := f.ImportTypes[contentType]
	return ok
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

// resolveContentType decides a stream's type. An operator's explicit verdict
// for the stream's group outranks everything, then the per-source category
// patterns apply, and the shared resolver settles what neither covers.
//
// A matching category pattern deliberately outranks the type the importer
// stamped on the stream: every importer already stamps an explicit type, so a
// pattern that only filled in the gaps would never fire. Series is tested
// first and live last, from most to least specific — series entries routinely
// also carry "movie" or "vod" in their URL path, and live is the type every
// unrecognised entry already falls back to.
func resolveContentType(stream *types.Stream, filter *CompiledFilter, subjects []string, groupKey string) types.ContentType {
	if forced, ok := filter.GroupTypes[groupKey]; ok {
		return forced
	}
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

// Options tunes a pass. RuleStats makes the walk continue past the rule that
// decided a stream so the report can say how many streams every later rule
// would have matched; the import path leaves it off and short-circuits.
// Verdicts records every stream's outcome in Report.Verdicts for the preview's
// result browser; the import path leaves it off since a catalog can run to
// hundreds of thousands of entries.
type Options struct {
	RuleStats bool
	Verdicts  bool
}

// Apply runs a source's filters over its raw catalog. It returns the streams
// that survive, each stamped with the content type it was filtered as, and a
// Report of what every group and rule contributed. The report is what the
// import persists as the source's inventory and what the admin preview shows
// before a save. The config supplies the shared rule profiles a source may
// name and may be nil when there are none.
//
// Each stage can only narrow, and they run in this order: the ordered rule
// list, the group filter, the content type gate, then that type's legacy
// include and exclude patterns. Classification happens first regardless,
// because the type stamp matters downstream: utils.ContentTypeOfStream returns
// an explicit type verbatim, so playlist output, XC catalogs and playback
// routing all serve the stream as the same type it was filtered as.
func Apply(streams []*types.Stream, source *config.SourceConfig, cfg *config.Config, filterManager *FilterManager, opts Options) ([]*types.Stream, *Report) {
	report := newReport()

	// a source without filters still needs the pass for its inventory, but not
	// the per-stream pattern work
	if !source.HasContentFilters() {
		logger.Debug("{filter - Apply} No filters configured for source %s, keeping %d streams\n", source.Name, len(streams))
		if opts.Verdicts {
			report.Verdicts = make([]Verdict, 0, len(streams))
		}
		for _, stream := range streams {
			group := GroupOf(stream)
			stream.ContentType = utils.ContentTypeOfStream(stream)
			report.observe(group, config.GroupKey(group), stream.ContentType, stream.Name, true)
			if opts.Verdicts {
				report.Verdicts = append(report.Verdicts, Verdict{Name: stream.Name, Group: group, Type: stream.ContentType, Kept: true, Stage: StageDefault})
			}
		}
		report.finish()
		return streams, report
	}

	logger.Debug("{filter - Apply} Applying filters to %d streams from source %s\n", len(streams), source.Name)
	filter := filterManager.GetOrCreateFilter(source, cfg)
	report.startRules(filter, opts.RuleStats)
	outcomes := make([]outcome, 0, len(streams))

	for _, stream := range streams {
		group := GroupOf(stream)
		groupKey := config.GroupKey(group)
		subjects := matchSubjects(stream)
		contentType := resolveContentType(stream, filter, subjects, groupKey)
		stream.ContentType = contentType

		// the rules settle a stream first; the later stages can only remove
		// what the rules let through, and the verdict names whichever did
		keep, decidedBy := report.observeRules(filter, stream, group, opts.RuleStats)
		stage := StageDefault
		if decidedBy >= 0 {
			stage = StageRule
		}
		if keep {
			switch {
			case !filter.passesGroupFilter(group, groupKey):
				keep, stage = false, StageGroup
			case !filter.importsType(contentType):
				keep, stage = false, StageType
			case !shouldIncludeStream(stream, filter, contentType, subjects):
				keep, stage = false, StagePattern
			}
		}
		outcomes = append(outcomes, outcome{stream: stream, group: group, groupKey: groupKey, contentType: contentType, keep: keep, stage: stage, rule: decidedBy + 1})
	}

	// the quality stage needs every verdict in hand: whether "A&E HD" stays
	// depends on whether "A&E FHD" survived the stages above
	report.QualityDropped = applyQualityDedupe(outcomes, filter)

	kept := make([]*types.Stream, 0, len(outcomes))
	if opts.Verdicts {
		report.Verdicts = make([]Verdict, 0, len(outcomes))
	}
	for _, o := range outcomes {
		logger.Debug("{filter - Apply} Stream: %s, Group: %s, Type: %s, Include: %v\n", o.stream.Name, o.group, o.contentType, o.keep)
		report.observe(o.group, o.groupKey, o.contentType, o.stream.Name, o.keep)
		if opts.Verdicts {
			report.Verdicts = append(report.Verdicts, Verdict{Name: o.stream.Name, Group: o.group, Type: o.contentType, Kept: o.keep, Stage: o.stage, Rule: o.rule})
		}
		if o.keep {
			kept = append(kept, o.stream)
		}
	}
	report.finish()
	logger.Debug("{filter - Apply} Filtered %d -> %d streams for source %s\n", len(streams), len(kept), source.Name)

	return kept, report
}

// outcome is one stream's verdict while a pass is still in flight, before it
// is written into the report: the quality stage needs the whole catalog's
// verdicts before any of them is final.
type outcome struct {
	stream      *types.Stream
	group       string
	groupKey    string
	contentType types.ContentType
	keep        bool
	stage       string
	rule        int // 1-based deciding rule, 0 when none
}

// qualityRank places a stream name in the source's quality tiers: the index of
// the first tier whose marker the name carries (0 is best) and the name with
// that marker removed, lowercased and with stray separators trimmed, which is
// what sibling variants are matched on. ok is false when no tier matches, so
// a name without a quality marker never takes part in the stage.
func (f *CompiledFilter) qualityRank(name string) (rank int, base string, ok bool) {
	for i, tier := range f.QualityTiers {
		loc := tier.FindStringIndex(name)
		if loc == nil {
			continue
		}
		stripped := name[:loc[0]] + " " + name[loc[1]:]
		base = strings.Join(strings.Fields(strings.ToLower(stripped)), " ")
		base = strings.Trim(base, " -|[]()/:·")
		return i, base, true
	}
	return 0, "", false
}

// applyQualityDedupe drops, among the streams still kept, every variant of a
// channel that a better-quality variant of the same content type outranks. A
// stream carrying no quality marker, or whose better siblings were dropped by
// an earlier stage, is left alone: HD only goes when FHD is actually there.
// Returns how many streams it dropped.
func applyQualityDedupe(outcomes []outcome, filter *CompiledFilter) int {
	if len(filter.QualityTiers) == 0 {
		return 0
	}

	type mark struct {
		rank int
		key  string
	}
	marks := make([]mark, len(outcomes))
	best := make(map[string]int)
	for i := range outcomes {
		marks[i].rank = -1
		o := &outcomes[i]
		if !o.keep {
			continue
		}
		rank, base, ok := filter.qualityRank(o.stream.Name)
		if !ok {
			continue
		}
		// typed, so a film and a channel that share a name never compete
		key := string(o.contentType) + "\x00" + base
		marks[i] = mark{rank: rank, key: key}
		if b, seen := best[key]; !seen || rank < b {
			best[key] = rank
		}
	}

	dropped := 0
	for i := range outcomes {
		m := marks[i]
		if m.rank < 0 || m.rank <= best[m.key] {
			continue
		}
		outcomes[i].keep = false
		outcomes[i].stage = StageQuality
		dropped++
	}
	return dropped
}

// FilterStreams applies a source's filters and returns the surviving streams.
// It is Apply without the report, for callers that only need the result.
func FilterStreams(streams []*types.Stream, source *config.SourceConfig, cfg *config.Config, filterManager *FilterManager) []*types.Stream {
	kept, _ := Apply(streams, source, cfg, filterManager, Options{})
	return kept
}

// shouldIncludeStream determines if a stream should be included based on the
// include/exclude patterns for the content type it was classified as.
func shouldIncludeStream(stream *types.Stream, filter *CompiledFilter, contentType types.ContentType, subjects []string) bool {
	originalName := stream.Name

	// Check include filters first - if any exist, stream must match at least one
	var hasIncludeFilters bool
	var matchesInclude bool

	switch contentType {
	case types.ContentTypeLive:
		if filter.LiveInclude != nil {
			hasIncludeFilters = true
			matchesInclude = matchesAny(filter.LiveInclude, subjects)
		}
	case types.ContentTypeSeries:
		if filter.SeriesInclude != nil {
			hasIncludeFilters = true
			matchesInclude = matchesAny(filter.SeriesInclude, subjects)
		}
	case types.ContentTypeVOD:
		if filter.VODInclude != nil {
			hasIncludeFilters = true
			matchesInclude = matchesAny(filter.VODInclude, subjects)
		}
	}

	// If include filters exist but stream doesn't match any, exclude it
	if hasIncludeFilters && !matchesInclude {
		logger.Debug("{filter - shouldIncludeStream} EXCLUDED by include filters: '%s'\n", originalName)
		return false
	}

	// Then check exclude filters
	switch contentType {
	case types.ContentTypeLive:
		if filter.LiveExclude != nil && matchesAny(filter.LiveExclude, subjects) {
			logger.Debug("{filter - shouldIncludeStream} EXCLUDED by live exclude filter: '%s'\n", originalName)
			return false
		}
	case types.ContentTypeSeries:
		if filter.SeriesExclude != nil && matchesAny(filter.SeriesExclude, subjects) {
			logger.Debug("{filter - shouldIncludeStream} EXCLUDED by series exclude filter: '%s'\n", originalName)
			return false
		}
	case types.ContentTypeVOD:
		if filter.VODExclude != nil && matchesAny(filter.VODExclude, subjects) {
			logger.Debug("{filter - shouldIncludeStream} EXCLUDED by VOD exclude filter: '%s'\n", originalName)
			return false
		}
	}

	return true
}
