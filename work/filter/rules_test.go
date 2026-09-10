package filter

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/types"
	"testing"
)

// rule is a shorthand for one ordered rule.
func rule(field, action, pattern string) config.FilterRule {
	return config.FilterRule{Field: field, Action: action, Pattern: pattern}
}

func withRules(def string, rules ...config.FilterRule) *config.SourceConfig {
	return &config.SourceConfig{URL: "http://p/list.m3u", FilterRules: rules, FilterDefault: def}
}

func TestRulesDecideByFirstMatch(t *testing.T) {
	// keep the live families, then drop the SD and alternate feeds among them
	src := withRules(config.FilterDefaultDrop,
		rule("name", "exclude", ` sd$|\[alt\]`),
		rule("group", "include", `^♦️  `),
	)
	assertNames(t, run(t, src, catalog()...), "GLOBO SP FHD", "SBT SP FHD")

	// swapping the order changes the outcome: the include now decides first
	swapped := withRules(config.FilterDefaultDrop,
		rule("group", "include", `^♦️  `),
		rule("name", "exclude", ` sd$|\[alt\]`),
	)
	assertNames(t, run(t, swapped, catalog()...), "GLOBO SP FHD", "SBT SP FHD")
}

func TestDefaultDropMakesRulesAnAllowList(t *testing.T) {
	src := withRules(config.FilterDefaultDrop, rule("group", "include", "sbt"))
	assertNames(t, run(t, src, catalog()...), "SBT SP FHD")
}

func TestDefaultKeepMakesRulesADenyList(t *testing.T) {
	src := withRules("", rule("group", "exclude", "novelas|ação|24h"))
	assertNames(t, run(t, src, catalog()...), "GLOBO SP FHD", "SBT SP FHD", "Orphan Feed")
}

func TestRulePatternsAreCaseInsensitiveAndFieldScoped(t *testing.T) {
	// the label is uppercase in the playlist and lowercase in the rule
	byGroup := withRules(config.FilterDefaultDrop, rule("group", "include", "globo"))
	assertNames(t, run(t, byGroup, catalog()...), "GLOBO SP FHD")

	// the same text in the name field must not match the group
	byName := withRules(config.FilterDefaultDrop, rule("name", "include", "^globo"))
	assertNames(t, run(t, byName, catalog()...), "GLOBO SP FHD")

	// a URL rule sees neither
	byURL := withRules(config.FilterDefaultDrop, rule("url", "include", `/play/a/ts$`))
	assertNames(t, run(t, byURL, catalog()...), "GLOBO SP FHD")

	// any spans all three
	byAny := withRules(config.FilterDefaultDrop, rule("any", "include", `duna|/play/f/ts`))
	assertNames(t, run(t, byAny, catalog()...), "Duna", "Orphan Feed")

	// an operator's own flag group is respected, so a case-sensitive rule is possible
	sensitive := withRules(config.FilterDefaultDrop, rule("group", "include", "(?-i)globo"))
	assertNames(t, run(t, sensitive, catalog()...))
}

func TestRulesCanNameTheEmptyGroup(t *testing.T) {
	// a stream with no group is matched by a rule anchored to an empty label
	src := withRules(config.FilterDefaultDrop, rule("name", "include", "^orphan"))
	assertNames(t, run(t, src, catalog()...), "Orphan Feed")
}

func TestRuleReportAttributesDecisionsAndShadowing(t *testing.T) {
	cfg := &config.Config{FilterProfiles: []config.FilterProfile{{
		Name:  "shared",
		Rules: []config.FilterRule{rule("group", "exclude", "novelas")},
	}}}
	src := &config.SourceConfig{
		URL:           "http://p/list.m3u",
		FilterProfile: "shared",
		FilterDefault: config.FilterDefaultDrop,
		FilterRules: []config.FilterRule{
			rule("group", "include", `^♦️  `),
			rule("group", "include", "globo"), // shadowed by the rule above
		},
	}

	kept, report := Apply(catalog(), src, cfg, NewFilterManager(), Options{RuleStats: true})
	if len(kept) != 2 {
		t.Fatalf("want the two double-space groups, got %d", len(kept))
	}
	if len(report.Rules) != 3 {
		t.Fatalf("the profile's rule must be reported with the source's, got %d", len(report.Rules))
	}
	if report.Rules[0].Origin != "profile:shared" || report.Rules[1].Origin != "source" {
		t.Fatalf("origins wrong: %q, %q", report.Rules[0].Origin, report.Rules[1].Origin)
	}
	if report.Rules[0].Decided != 1 || report.Rules[0].Matched != 1 {
		t.Fatalf("the profile rule should have dropped the one Novelas entry: %+v", report.Rules[0])
	}
	if report.Rules[1].Decided != 2 {
		t.Fatalf("the prefix rule should have kept two: %+v", report.Rules[1])
	}
	// the shadowed rule decided nothing but did match, which is how an
	// operator tells "wrong pattern" from "already covered"
	if report.Rules[2].Decided != 0 || report.Rules[2].Matched != 1 {
		t.Fatalf("shadowed rule stats wrong: %+v", report.Rules[2])
	}
	if report.DefaultAction != config.FilterDefaultDrop || report.DefaultDecided != 3 {
		t.Fatalf("default stats wrong: %q decided %d", report.DefaultAction, report.DefaultDecided)
	}
	if report.Kept != 2 || report.Total != 6 {
		t.Fatalf("totals wrong: %d of %d", report.Kept, report.Total)
	}
}

func TestRuleStatsOffOnlyCountsDecisions(t *testing.T) {
	src := withRules(config.FilterDefaultDrop,
		rule("group", "include", `^♦️  `),
		rule("group", "include", "globo"),
	)
	_, report := Apply(catalog(), src, nil, NewFilterManager(), Options{})
	if report.Rules[1].Decided != 0 || report.Rules[1].Matched != 0 {
		t.Fatalf("without the extra pass a shadowed rule reports only its decisions: %+v", report.Rules[1])
	}
}

func TestProfileRulesRunBeforeTheSourcesOwn(t *testing.T) {
	// the profile keeps the live families; the source then drops one of them
	cfg := &config.Config{FilterProfiles: []config.FilterProfile{{
		Name:    "live-only",
		Default: config.FilterDefaultDrop,
		Rules:   []config.FilterRule{rule("group", "include", `^♦️  `)},
	}}}
	src := &config.SourceConfig{
		URL:           "http://p/list.m3u",
		FilterProfile: "live-only",
		FilterRules:   []config.FilterRule{rule("group", "exclude", "sbt")},
	}
	// the profile's include decides first, so the source's exclude never fires
	kept := FilterStreams(catalog(), src, cfg, NewFilterManager())
	assertNames(t, kept, "GLOBO SP FHD", "SBT SP FHD")

	// ordering the source's rule first is what an operator wants here, and the
	// source list is appended, so the exclusion belongs in the profile or the
	// source must repeat the include after it
	src.FilterRules = []config.FilterRule{
		rule("group", "exclude", "sbt"),
		rule("group", "include", `^♦️  `),
	}
	src.FilterProfile = ""
	src.FilterDefault = config.FilterDefaultDrop
	assertNames(t, FilterStreams(catalog(), src, cfg, NewFilterManager()), "GLOBO SP FHD")
}

func TestProfileDefaultAppliesUnlessTheSourceChoosesOne(t *testing.T) {
	cfg := &config.Config{FilterProfiles: []config.FilterProfile{{
		Name:    "drop-rest",
		Default: config.FilterDefaultDrop,
		Rules:   []config.FilterRule{rule("group", "include", "sbt")},
	}}}
	src := &config.SourceConfig{URL: "http://p/list.m3u", FilterProfile: "drop-rest"}
	assertNames(t, FilterStreams(catalog(), src, cfg, NewFilterManager()), "SBT SP FHD")

	// the source overrides it, turning the same profile into a deny list
	src.FilterDefault = config.FilterDefaultKeep
	if got := FilterStreams(catalog(), src, cfg, NewFilterManager()); len(got) != 6 {
		t.Fatalf("keep should let the unmatched streams through, got %d", len(got))
	}
}

func TestMissingProfileLeavesTheSourcesOwnRules(t *testing.T) {
	cfg := &config.Config{}
	src := &config.SourceConfig{
		URL:           "http://p/list.m3u",
		FilterProfile: "deleted",
		FilterDefault: config.FilterDefaultDrop,
		FilterRules:   []config.FilterRule{rule("group", "include", "sbt")},
	}
	assertNames(t, FilterStreams(catalog(), src, cfg, NewFilterManager()), "SBT SP FHD")
}

func TestRuleEditsInvalidateTheCachedFilter(t *testing.T) {
	fm := NewFilterManager()
	cfg := &config.Config{FilterProfiles: []config.FilterProfile{{
		Name:  "shared",
		Rules: []config.FilterRule{rule("group", "exclude", "novelas")},
	}}}
	src := &config.SourceConfig{URL: "http://p/list.m3u", FilterProfile: "shared"}

	first := fm.GetOrCreateFilter(src, cfg)
	if fm.GetOrCreateFilter(src, cfg) != first {
		t.Fatal("an unchanged source must reuse its compiled filter")
	}

	// editing the profile the source names has to recompile it
	cfg.FilterProfiles[0].Rules = append(cfg.FilterProfiles[0].Rules, rule("name", "exclude", "sd$"))
	second := fm.GetOrCreateFilter(src, cfg)
	if second == first {
		t.Fatal("a changed profile must recompile the filter")
	}

	src.FilterRules = []config.FilterRule{rule("group", "include", "globo")}
	third := fm.GetOrCreateFilter(src, cfg)
	if third == second {
		t.Fatal("a changed rule list must recompile the filter")
	}

	src.FilterDefault = config.FilterDefaultDrop
	if fm.GetOrCreateFilter(src, cfg) == third {
		t.Fatal("a changed default must recompile the filter")
	}
}

func TestRulesComposeWithTheOtherStages(t *testing.T) {
	// every stage narrows: the rules keep two, the type gate then drops the
	// one the operator reclassified as VOD
	src := &config.SourceConfig{
		URL:                "http://p/list.m3u",
		FilterDefault:      config.FilterDefaultDrop,
		FilterRules:        []config.FilterRule{rule("group", "include", `^♦️  `)},
		ImportTypes:        []string{"live"},
		GroupTypeOverrides: map[string]string{"♦️  SBT": "vod"},
	}
	out := run(t, src, catalog()...)
	assertNames(t, out, "GLOBO SP FHD")
	if out[0].ContentType != types.ContentTypeLive {
		t.Fatalf("the survivor must keep its type stamp, got %q", out[0].ContentType)
	}
}
