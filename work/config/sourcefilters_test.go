package config

import (
	"strings"
	"testing"
)

func TestNormalizeFiltersRejectsBadInputByField(t *testing.T) {
	cases := []struct {
		name string
		src  *SourceConfig
		want string
	}{
		{"mode", &SourceConfig{Name: "p", GroupFilterMode: "only"}, "groupFilterMode"},
		{"empty include", &SourceConfig{Name: "p", GroupFilterMode: GroupFilterInclude}, "keeps nothing"},
		{"type", &SourceConfig{Name: "p", ImportTypes: []string{"radio"}}, "importTypes"},
		{"override", &SourceConfig{Name: "p", GroupTypeOverrides: map[string]string{"g": "movie"}}, "groupTypeOverrides"},
		{"group regex", &SourceConfig{Name: "p", GroupFilterRegex: "("}, "groupFilterRegex"},
		{"live include", &SourceConfig{Name: "p", LiveIncludeRegex: "["}, "liveIncludeRegex"},
	}
	for _, tc := range cases {
		err := tc.src.NormalizeFilters()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: want error mentioning %q, got %v", tc.name, tc.want, err)
		}
	}
}

func TestNormalizeFiltersTidiesInput(t *testing.T) {
	src := SourceConfig{
		Name:               "p",
		GroupFilterMode:    GroupFilterExclude,
		GroupFilterList:    []string{" ♦️  GLOBO ", "♦️ globo", "", "♦️  SBT"},
		GroupFilterRegex:   " ^x ",
		ImportTypes:        []string{"Live", "live", " VOD "},
		GroupTypeOverrides: map[string]string{" g ": " Live", "": "vod", "h": ""},
	}
	if err := src.NormalizeFilters(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// the empty label is a selection of its own, so it survives beside the rest
	if len(src.GroupFilterList) != 3 || src.GroupFilterList[0] != "♦️  GLOBO" ||
		src.GroupFilterList[1] != "" || src.GroupFilterList[2] != "♦️  SBT" {
		t.Fatalf("list should be trimmed and deduped by GroupKey, got %q", src.GroupFilterList)
	}
	// a pattern is kept verbatim: whitespace is significant in a regex
	if src.GroupFilterRegex != " ^x " {
		t.Fatalf("regex should be preserved, got %q", src.GroupFilterRegex)
	}
	if len(src.ImportTypes) != 2 || src.ImportTypes[0] != "live" || src.ImportTypes[1] != "vod" {
		t.Fatalf("types should be lowercased and deduped, got %q", src.ImportTypes)
	}
	if len(src.GroupTypeOverrides) != 1 || src.GroupTypeOverrides["g"] != "live" {
		t.Fatalf("overrides should be trimmed and blanks dropped, got %q", src.GroupTypeOverrides)
	}
}

func TestNormalizeFiltersTreatsEveryTypeAsNoGate(t *testing.T) {
	src := SourceConfig{Name: "p", ImportTypes: []string{"series", "vod", "live"}}
	if err := src.NormalizeFilters(); err != nil {
		t.Fatal(err)
	}
	if src.ImportTypes != nil {
		t.Fatalf("all three types is the same as no gate and must be stored that way, got %q", src.ImportTypes)
	}
	if !src.ImportsType("vod") {
		t.Fatal("no gate imports everything")
	}
	gated := SourceConfig{ImportTypes: []string{"live"}}
	if gated.ImportsType("vod") || !gated.ImportsType("live") {
		t.Fatal("gate should admit only listed types")
	}
}

func TestFilterFieldsRoundTripThroughDBEncoding(t *testing.T) {
	list := []string{"♦️  GLOBO", "Canais | Abertos"}
	if got := decodeStringList(encodeStringList(list)); len(got) != 2 || got[0] != list[0] || got[1] != list[1] {
		t.Fatalf("list round trip lost data: %q", got)
	}
	if encodeStringList(nil) != "" || decodeStringList("") != nil || decodeStringList("not json") != nil {
		t.Fatal("empty and malformed text must read as no list")
	}
	m := map[string]string{"♦️ CANAIS 24H | SERIES ⭐": "live"}
	if got := decodeStringMap(encodeStringMap(m)); len(got) != 1 || got["♦️ CANAIS 24H | SERIES ⭐"] != "live" {
		t.Fatalf("map round trip lost data: %q", got)
	}
	if encodeStringMap(nil) != "" || decodeStringMap("") != nil || decodeStringMap("{") != nil {
		t.Fatal("empty and malformed text must read as no map")
	}
}

func TestParseSourceJSONReadsFilterFields(t *testing.T) {
	var src SourceConfig
	err := ParseSourceJSON([]byte(`{"name":"p","url":"http://x/l.m3u","maxStreamTimeout":"30s",
		"groupFilterMode":"include","groupFilterList":["♦️  GLOBO"],"groupFilterRegex":"^♦️  ",
		"importTypes":["live"],"groupTypeOverrides":{"♦️ CANAIS 24H | SERIES ⭐":"live"}}`), &src)
	if err != nil {
		t.Fatal(err)
	}
	if src.GroupFilterMode != GroupFilterInclude || len(src.GroupFilterList) != 1 || src.GroupFilterRegex != "^♦️  " ||
		len(src.ImportTypes) != 1 || src.GroupTypeOverrides["♦️ CANAIS 24H | SERIES ⭐"] != "live" || src.MaxStreamTimeout.Seconds() != 30 {
		t.Fatalf("fields not decoded: mode=%q list=%q regex=%q types=%q overrides=%q", src.GroupFilterMode, src.GroupFilterList, src.GroupFilterRegex, src.ImportTypes, src.GroupTypeOverrides)
	}
	if err := ParseSourceJSON([]byte(`{"name":"p","retryDelay":"soon"}`), &src); err == nil {
		t.Fatal("bad duration must be rejected")
	}
}

func TestGroupKeyCollapsesCaseAndWhitespace(t *testing.T) {
	if GroupKey("  ♦️   GLOBO ") != "♦️ globo" || GroupKey("A\tB") != "a b" || GroupKey("") != "" {
		t.Fatal("GroupKey must lowercase, trim and collapse whitespace")
	}
}

func TestNormalizeFiltersKeepsTheNoGroupSelection(t *testing.T) {
	// "(no group)" is a real row in the picker: it is how streams that carry
	// no group-title are named, and the engine keys it like any other label
	src := SourceConfig{Name: "p", GroupFilterMode: GroupFilterInclude, GroupFilterList: []string{"", "♦️  SBT", "  "}}
	if err := src.NormalizeFilters(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(src.GroupFilterList) != 2 || src.GroupFilterList[0] != "" || src.GroupFilterList[1] != "♦️  SBT" {
		t.Fatalf("empty label must survive and collapse with whitespace-only, got %q", src.GroupFilterList)
	}

	only := SourceConfig{Name: "p", GroupFilterMode: GroupFilterInclude, GroupFilterList: []string{""}}
	if err := only.NormalizeFilters(); err != nil {
		t.Fatalf("a list holding only the empty label is a valid selection: %v", err)
	}
}

func TestNormalizeFiltersRejectsConflictingOverrideSpellings(t *testing.T) {
	// both spellings name one group; forcing two types would resolve at random
	conflict := SourceConfig{Name: "p", GroupTypeOverrides: map[string]string{"♦️  GLOBO": "live", "♦️ globo": "vod"}}
	err := conflict.NormalizeFilters()
	if err == nil || !strings.Contains(err.Error(), "same group twice") {
		t.Fatalf("want a conflict error, got %v", err)
	}

	// the same type twice is not a conflict, it collapses to one entry
	agree := SourceConfig{Name: "p", GroupTypeOverrides: map[string]string{"♦️  GLOBO": "live", "♦️ globo": "live"}}
	if err := agree.NormalizeFilters(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agree.GroupTypeOverrides) != 1 {
		t.Fatalf("equal spellings should collapse, got %q", agree.GroupTypeOverrides)
	}
}

func TestFiltersEqualComparesEveryRule(t *testing.T) {
	base := func() SourceConfig {
		return SourceConfig{
			GroupFilterMode:    GroupFilterInclude,
			GroupFilterList:    []string{"a", "b"},
			GroupFilterRegex:   "^x",
			ImportTypes:        []string{"live"},
			GroupTypeOverrides: map[string]string{"g": "live"},
			LiveIncludeRegex:   "fhd$",
		}
	}
	a, b := base(), base()
	if !FiltersEqual(&a, &b) {
		t.Fatal("identical filters must compare equal")
	}
	// a difference in any one rule has to be noticed
	mutations := []func(*SourceConfig){
		func(s *SourceConfig) { s.GroupFilterMode = GroupFilterExclude },
		func(s *SourceConfig) { s.GroupFilterList = []string{"a"} },
		func(s *SourceConfig) { s.GroupFilterList = []string{"b", "a"} },
		func(s *SourceConfig) { s.GroupFilterRegex = "^y" },
		func(s *SourceConfig) { s.ImportTypes = nil },
		func(s *SourceConfig) { s.GroupTypeOverrides = map[string]string{"g": "vod"} },
		func(s *SourceConfig) { s.LiveIncludeRegex = "" },
		func(s *SourceConfig) { s.VODExcludeRegex = "adult" },
	}
	for i, mutate := range mutations {
		changed := base()
		mutate(&changed)
		if FiltersEqual(&a, &changed) {
			t.Fatalf("mutation %d should not compare equal", i)
		}
	}
	// fields outside the filters are irrelevant
	renamed := base()
	renamed.Name, renamed.MaxConnections = "other", 99
	if !FiltersEqual(&a, &renamed) {
		t.Fatal("non-filter fields must not affect the comparison")
	}
}

func TestNormalizeRulesTidiesAndValidates(t *testing.T) {
	rules, err := NormalizeRules([]FilterRule{
		{Field: " GROUP ", Action: " Include ", Pattern: "  ^globo  ", Note: " keep the network "},
		{Pattern: "sd$"},             // field and action default to group/include
		{Field: "name", Pattern: ""}, // half-typed: dropped rather than matching everything
	}, "source \"p\"")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("the empty pattern must be dropped, got %d rules", len(rules))
	}
	if rules[0].Field != FilterFieldGroup || rules[0].Action != FilterActionInclude ||
		rules[0].Pattern != "  ^globo  " || rules[0].Note != "keep the network" {
		t.Fatalf("rule not tidied: %+v", rules[0])
	}
	if rules[1].Field != FilterFieldGroup || rules[1].Action != FilterActionInclude {
		t.Fatalf("defaults wrong: %+v", rules[1])
	}

	for _, tc := range []struct {
		name string
		rule FilterRule
		want string
	}{
		{"field", FilterRule{Field: "title", Pattern: "x"}, "field"},
		{"action", FilterRule{Action: "drop", Pattern: "x"}, "action"},
		{"pattern", FilterRule{Pattern: "("}, "not valid"},
		{"length", FilterRule{Pattern: strings.Repeat("a", MaxFilterPatternLen+1)}, "longer than"},
	} {
		if _, err := NormalizeRules([]FilterRule{tc.rule}, "source \"p\""); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: want an error mentioning %q, got %v", tc.name, tc.want, err)
		}
	}

	// the position in the list is what an operator sees, so it is named
	_, err = NormalizeRules([]FilterRule{{Pattern: "ok"}, {Pattern: "["}}, "source \"p\"")
	if err == nil || !strings.Contains(err.Error(), "rule 2") {
		t.Fatalf("want the rule position in the message, got %v", err)
	}

	// a list nobody could have meant is refused rather than silently trimmed
	many := make([]FilterRule, MaxFilterRules+1)
	for i := range many {
		many[i] = FilterRule{Pattern: "x"}
	}
	if _, err := NormalizeRules(many, "source \"p\""); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("want a cap error, got %v", err)
	}
}

func TestCompileFilterPatternIsCaseInsensitiveUnlessTold(t *testing.T) {
	insensitive, err := CompileFilterPattern("globo")
	if err != nil {
		t.Fatal(err)
	}
	if !insensitive.MatchString("♦️  GLOBO") {
		t.Fatal("a pattern must match whatever case the provider used")
	}

	sensitive, err := CompileFilterPattern("(?-i)globo")
	if err != nil {
		t.Fatal(err)
	}
	if sensitive.MatchString("♦️  GLOBO") {
		t.Fatal("an explicit flag group must be honoured")
	}
}

func TestProfileNormalizeValidatesNameDefaultAndRules(t *testing.T) {
	blank := FilterProfile{Name: "  "}
	if err := blank.Normalize(); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("a profile needs a name, got %v", err)
	}

	bad := FilterProfile{Name: "p", Default: "maybe"}
	if err := bad.Normalize(); err == nil || !strings.Contains(err.Error(), "default") {
		t.Fatalf("want a default error, got %v", err)
	}

	p := FilterProfile{Name: " live only ", Default: " KEEP ", Rules: []FilterRule{{Pattern: " ^canais "}}}
	if err := p.Normalize(); err != nil {
		t.Fatal(err)
	}
	if p.Name != "live only" {
		t.Fatalf("name not trimmed: %q", p.Name)
	}
	// keep is the implied verdict, so it is stored as the empty value
	if p.Default != "" {
		t.Fatalf("keep should normalize to empty, got %q", p.Default)
	}
	if len(p.Rules) != 1 || p.Rules[0].Pattern != " ^canais " {
		t.Fatalf("rules not normalized: %+v", p.Rules)
	}
}

func TestEffectiveRulesAndDefaultResolveTheProfile(t *testing.T) {
	cfg := &Config{FilterProfiles: []FilterProfile{
		{Name: "shared", Default: FilterDefaultDrop, Rules: []FilterRule{{Field: FilterFieldGroup, Action: FilterActionInclude, Pattern: "a"}}},
	}}
	src := &SourceConfig{
		FilterProfile: "shared",
		FilterRules:   []FilterRule{{Field: FilterFieldName, Action: FilterActionExclude, Pattern: "b"}},
	}

	rules, origins := cfg.EffectiveRules(src)
	if len(rules) != 2 || rules[0].Pattern != "a" || rules[1].Pattern != "b" {
		t.Fatalf("the profile's rules must come first: %+v", rules)
	}
	if origins[0] != "profile:shared" || origins[1] != "source" {
		t.Fatalf("origins wrong: %v", origins)
	}
	if cfg.EffectiveDefault(src) != FilterDefaultDrop {
		t.Fatal("the profile's default applies when the source has none")
	}

	src.FilterDefault = FilterDefaultKeep
	if cfg.EffectiveDefault(src) != FilterDefaultKeep {
		t.Fatal("the source's own default wins")
	}

	// a profile that no longer exists leaves the source's own rules alone
	src.FilterProfile = "gone"
	rules, origins = cfg.EffectiveRules(src)
	if len(rules) != 1 || origins[0] != "source" {
		t.Fatalf("want only the source's rule, got %+v (%v)", rules, origins)
	}
	if cfg.EffectiveDefault(&SourceConfig{FilterProfile: "gone"}) != FilterDefaultKeep {
		t.Fatal("keep is the fallback when nothing else says otherwise")
	}
}

func TestRulesRoundTripThroughDBEncoding(t *testing.T) {
	rules := []FilterRule{
		{Field: FilterFieldGroup, Action: FilterActionInclude, Pattern: "^♦️  ", Note: "live families"},
		{Field: FilterFieldName, Action: FilterActionExclude, Pattern: `\[alt\]`},
	}
	got := DecodeRules(EncodeRules(rules))
	if len(got) != 2 || got[0].Pattern != "^♦️  " || got[0].Note != "live families" || got[1].Action != FilterActionExclude {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if EncodeRules(nil) != "" || DecodeRules("") != nil || DecodeRules("not json") != nil {
		t.Fatal("empty and malformed text must read as no rules")
	}
}

func TestSourceWithOnlyRulesCountsAsFiltered(t *testing.T) {
	// the engine's fast path must not skip a source whose only filter is a
	// rule list, a named profile, or a drop-by-default verdict
	for _, src := range []*SourceConfig{
		{FilterRules: []FilterRule{{Pattern: "x"}}},
		{FilterProfile: "shared"},
		{FilterDefault: FilterDefaultDrop},
	} {
		if !src.HasContentFilters() {
			t.Fatalf("should count as filtered: profile=%q rules=%d default=%q", src.FilterProfile, len(src.FilterRules), src.FilterDefault)
		}
	}
	if (&SourceConfig{FilterDefault: FilterDefaultKeep}).HasContentFilters() {
		t.Fatal("keeping everything by default is not a filter")
	}
}

func TestPatternWhitespaceIsPreserved(t *testing.T) {
	// this provider marks its live TV groups with a double space after the
	// emoji, so "^♦️  " keeps 27 groups while the trimmed "^♦️" keeps 96 —
	// trimming a pattern silently turns one rule into the other
	src := SourceConfig{
		Name:             "p",
		GroupFilterRegex: "^♦️  ",
		FilterRules:      []FilterRule{{Field: FilterFieldGroup, Action: FilterActionInclude, Pattern: "^♦️  "}},
		FilterDefault:    FilterDefaultDrop,
	}
	if err := src.NormalizeFilters(); err != nil {
		t.Fatal(err)
	}
	if src.GroupFilterRegex != "^♦️  " {
		t.Fatalf("group pattern lost its trailing spaces: %q", src.GroupFilterRegex)
	}
	if src.FilterRules[0].Pattern != "^♦️  " {
		t.Fatalf("rule pattern lost its trailing spaces: %q", src.FilterRules[0].Pattern)
	}

	pattern, err := CompileFilterPattern(src.FilterRules[0].Pattern)
	if err != nil {
		t.Fatal(err)
	}
	if !pattern.MatchString("♦️  GLOBO") {
		t.Fatal("the double-space prefix must match a live group")
	}
	if pattern.MatchString("♦️ Netflix") {
		t.Fatal("the double-space prefix must not match a single-space catalogue group")
	}
}

func TestSourceJSONAndDBMappingCarryEveryFilterField(t *testing.T) {
	// the wire alias, the struct and the two DB mappings each list the fields
	// by hand, so a field added to one and forgotten in another reads back empty
	payload := []byte(`{"name":"p","url":"http://x/l.m3u","maxStreamTimeout":"30s","retryDelay":"5s",
		"filterProfile":"shared","filterDefault":"drop",
		"filterRules":[{"field":"name","action":"exclude","pattern":" sd$","note":"dupes"}],
		"groupFilterMode":"include","groupFilterList":["g"],"groupFilterRegex":"^x",
		"importTypes":["live"],"groupTypeOverrides":{"g":"live"}}`)

	var src SourceConfig
	if err := ParseSourceJSON(payload, &src); err != nil {
		t.Fatal(err)
	}
	if src.FilterProfile != "shared" || src.FilterDefault != FilterDefaultDrop || len(src.FilterRules) != 1 ||
		src.FilterRules[0].Pattern != " sd$" || src.FilterRules[0].Note != "dupes" {
		t.Fatalf("the wire alias dropped a rule field: profile=%q default=%q rules=%+v", src.FilterProfile, src.FilterDefault, src.FilterRules)
	}

	// the same document decoded as a whole config, which is what the admin API posts
	var cfg Config
	if err := cfg.UnmarshalJSON([]byte(`{"baseURL":"http://x","sources":[` + string(payload) + `]}`)); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) != 1 || cfg.Sources[0].FilterProfile != "shared" || len(cfg.Sources[0].FilterRules) != 1 {
		t.Fatalf("config decode dropped a rule field: %+v", cfg.Sources)
	}
}
