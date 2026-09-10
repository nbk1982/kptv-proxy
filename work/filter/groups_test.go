package filter

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/types"
	"testing"
)

func names(streams []*types.Stream) []string {
	out := make([]string, 0, len(streams))
	for _, s := range streams {
		out = append(out, s.Name)
	}
	return out
}

func assertNames(t *testing.T, got []*types.Stream, want ...string) {
	t.Helper()
	gotNames := names(got)
	if len(gotNames) != len(want) {
		t.Fatalf("want %v, got %v", want, gotNames)
	}
	for i := range want {
		if gotNames[i] != want[i] {
			t.Fatalf("want %v, got %v", want, gotNames)
		}
	}
}

// catalog mirrors the shape of a real XUI playlist: a handful of live groups
// with emoji prefixes and inconsistent spacing, buried in VOD and series.
func catalog() []*types.Stream {
	return []*types.Stream{
		stream("GLOBO SP FHD", "http://p/play/a/ts", "♦️  GLOBO", types.ContentTypeLive),
		stream("SBT SP FHD", "http://p/play/b/ts", "♦️  SBT", types.ContentTypeLive),
		stream("[24H] Big Mouth 05", "http://p/play/c/ts", "♦️ CANAIS 24H | SERIES ⭐", types.ContentTypeLive),
		stream("A Candidata S01E01", "http://p/play/d", "♦️ Novelas", types.ContentTypeSeries),
		stream("Duna", "http://p/play/e", "♦️ Ação ✔️", types.ContentTypeVOD),
		stream("Orphan Feed", "http://p/play/f/ts", "", types.ContentTypeLive),
	}
}

func TestIncludeModeKeepsOnlyListedGroups(t *testing.T) {
	src := &config.SourceConfig{
		GroupFilterMode: config.GroupFilterInclude,
		GroupFilterList: []string{"♦️  GLOBO", "♦️  SBT"},
	}
	assertNames(t, run(t, src, catalog()...), "GLOBO SP FHD", "SBT SP FHD")
}

func TestGroupListMatchesAcrossCaseAndSpacing(t *testing.T) {
	// the operator selected the label when it carried one space; the provider
	// now sends two, and in a different case
	src := &config.SourceConfig{
		GroupFilterMode: config.GroupFilterInclude,
		GroupFilterList: []string{"♦️ globo"},
	}
	assertNames(t, run(t, src, catalog()...), "GLOBO SP FHD")
}

func TestExcludeModeDropsListedGroups(t *testing.T) {
	src := &config.SourceConfig{
		GroupFilterMode: config.GroupFilterExclude,
		GroupFilterList: []string{"♦️ Novelas", "♦️ Ação ✔️"},
	}
	assertNames(t, run(t, src, catalog()...), "GLOBO SP FHD", "SBT SP FHD", "[24H] Big Mouth 05", "Orphan Feed")
}

func TestGroupRegexIsORedWithTheList(t *testing.T) {
	// the double-space prefix is how this provider marks live TV groups
	src := &config.SourceConfig{
		GroupFilterMode:  config.GroupFilterInclude,
		GroupFilterList:  []string{"♦️ Novelas"},
		GroupFilterRegex: "^♦️  ",
	}
	assertNames(t, run(t, src, catalog()...), "GLOBO SP FHD", "SBT SP FHD", "A Candidata S01E01")
}

func TestStreamsWithoutGroupCanBeListedAsEmptyLabel(t *testing.T) {
	src := &config.SourceConfig{GroupFilterMode: config.GroupFilterInclude, GroupFilterList: []string{""}}
	// NormalizeFilters would reject an empty-only list at the API; the engine
	// itself keys the label "" like any other so a mixed list works
	src.GroupFilterList = []string{"", "♦️  SBT"}
	assertNames(t, run(t, src, catalog()...), "SBT SP FHD", "Orphan Feed")

	drop := &config.SourceConfig{GroupFilterMode: config.GroupFilterInclude, GroupFilterList: []string{"♦️  SBT"}}
	assertNames(t, run(t, drop, catalog()...), "SBT SP FHD")
}

func TestImportTypesGateDropsOtherTypes(t *testing.T) {
	src := &config.SourceConfig{ImportTypes: []string{"live"}}
	out := run(t, src, catalog()...)
	// the importer stamped the 24H entry live, and the heuristics are not
	// consulted for a stamped stream, so it stays
	assertNames(t, out, "GLOBO SP FHD", "SBT SP FHD", "[24H] Big Mouth 05", "Orphan Feed")
}

func TestGroupTypeOverrideOutranksPatternsAndStamp(t *testing.T) {
	src := &config.SourceConfig{
		ImportTypes:         []string{"live"},
		SeriesCategoryRegex: "24h", // would reclassify the 24H entry as series
		GroupTypeOverrides:  map[string]string{"♦️ canais 24h | series ⭐": "live"},
	}
	out := run(t, src, catalog()...)
	assertNames(t, out, "GLOBO SP FHD", "SBT SP FHD", "[24H] Big Mouth 05", "Orphan Feed")
	for _, s := range out {
		if s.Name == "[24H] Big Mouth 05" && s.ContentType != types.ContentTypeLive {
			t.Fatalf("override should force live, got %q", s.ContentType)
		}
	}

	// the same pattern without the override does reclassify, and the gate then drops it
	noOverride := &config.SourceConfig{ImportTypes: []string{"live"}, SeriesCategoryRegex: "24h"}
	assertNames(t, run(t, noOverride, catalog()...), "GLOBO SP FHD", "SBT SP FHD", "Orphan Feed")
}

func TestStagesCombineWithAnd(t *testing.T) {
	// group filter, type gate and the name pattern all have to agree
	src := &config.SourceConfig{
		GroupFilterMode:  config.GroupFilterInclude,
		GroupFilterRegex: "^♦️  ",
		ImportTypes:      []string{"live"},
		LiveIncludeRegex: "fhd$",
		LiveExcludeRegex: "^sbt",
	}
	assertNames(t, run(t, src, catalog()...), "GLOBO SP FHD")
}

func TestReportCountsEveryGroupBeforeAndAfter(t *testing.T) {
	src := &config.SourceConfig{
		GroupFilterMode: config.GroupFilterInclude,
		GroupFilterList: []string{"♦️  GLOBO"},
	}
	kept, report := Apply(catalog(), src, NewFilterManager())
	if len(kept) != 1 || report.Total != 6 || report.Kept != 1 {
		t.Fatalf("kept=%d total=%d reportKept=%d", len(kept), report.Total, report.Kept)
	}
	if len(report.Groups) != 6 {
		t.Fatalf("every group including the empty one must be reported, got %d", len(report.Groups))
	}
	byName := make(map[string]*GroupStat)
	for _, g := range report.Groups {
		byName[g.Name] = g
	}
	if g := byName["♦️  GLOBO"]; g == nil || g.Total != 1 || g.Kept != 1 || g.ContentType != types.ContentTypeLive || len(g.Samples) != 1 {
		t.Fatalf("globo stat wrong: %+v", g)
	}
	if g := byName["♦️ Novelas"]; g == nil || g.Total != 1 || g.Kept != 0 || g.ContentType != types.ContentTypeSeries {
		t.Fatalf("novelas stat wrong: %+v", g)
	}
	if g := byName[""]; g == nil || g.Total != 1 {
		t.Fatalf("empty group must be reported: %+v", g)
	}
	if report.ByType[types.ContentTypeLive].Total != 4 || report.ByType[types.ContentTypeLive].Kept != 1 ||
		report.ByType[types.ContentTypeVOD].Total != 1 || report.ByType[types.ContentTypeSeries].Total != 1 {
		t.Fatalf("type totals wrong: %+v", report.ByType)
	}
	if len(report.KeptSamples) != 1 || report.KeptSamples[0] != "GLOBO SP FHD" {
		t.Fatalf("kept samples wrong: %v", report.KeptSamples)
	}
}

func TestReportWithoutFiltersStillInventoriesGroups(t *testing.T) {
	kept, report := Apply(catalog(), &config.SourceConfig{}, NewFilterManager())
	if len(kept) != 6 || report.Kept != 6 || len(report.Groups) != 6 {
		t.Fatalf("unfiltered pass must keep all and inventory every group: kept=%d groups=%d", len(kept), len(report.Groups))
	}
	// largest group first, ties broken by name
	if report.Groups[0].Total != 1 || report.Groups[0].Name != "" {
		t.Fatalf("groups should be ordered by size then name, got %q first", report.Groups[0].Name)
	}
}

func TestGroupRulesInvalidateCachedFilter(t *testing.T) {
	fm := NewFilterManager()
	src := &config.SourceConfig{URL: "http://p/list.m3u", GroupFilterMode: config.GroupFilterInclude, GroupFilterList: []string{"♦️  GLOBO"}}
	first := fm.GetOrCreateFilter(src)

	src.GroupFilterList = append(src.GroupFilterList, "♦️  SBT")
	if fm.GetOrCreateFilter(src) == first {
		t.Fatal("a changed group list must recompile the filter")
	}
	second := fm.GetOrCreateFilter(src)
	src.GroupTypeOverrides = map[string]string{"♦️  SBT": "vod"}
	if fm.GetOrCreateFilter(src) == second {
		t.Fatal("a changed override must recompile the filter")
	}
	third := fm.GetOrCreateFilter(src)
	src.ImportTypes = []string{"live"}
	if fm.GetOrCreateFilter(src) == third {
		t.Fatal("a changed type gate must recompile the filter")
	}
}
