package filter

import (
	"testing"

	"kptv-proxy/work/config"
	"kptv-proxy/work/types"
)

// verdicts runs a pass with per-stream outcomes recorded and returns them
// indexed by stream name.
func verdicts(t *testing.T, src *config.SourceConfig, in ...*types.Stream) map[string]Verdict {
	t.Helper()
	if err := src.NormalizeFilters(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	_, report := Apply(in, src, nil, NewFilterManager(), Options{RuleStats: true, Verdicts: true})
	if len(report.Verdicts) != len(in) {
		t.Fatalf("want %d verdicts, got %d", len(in), len(report.Verdicts))
	}
	out := make(map[string]Verdict, len(report.Verdicts))
	for _, v := range report.Verdicts {
		out[v.Name] = v
	}
	return out
}

func TestVerdictsNameTheStageThatSettledEachStream(t *testing.T) {
	src := &config.SourceConfig{
		FilterDefault: "keep",
		FilterRules: []config.FilterRule{
			{Field: "name", Action: "exclude", Pattern: " sd$"},
			{Field: "group", Action: "include", Pattern: "^news"},
		},
		GroupFilterMode:  "exclude",
		GroupFilterList:  []string{"Shopping"},
		ImportTypes:      []string{"live"},
		LiveExcludeRegex: "backup",
	}

	got := verdicts(t, src,
		stream("Globo SD", "http://p/live/1.ts", "News", types.ContentTypeLive),        // rule 1 drops
		stream("Globo HD", "http://p/live/2.ts", "News", types.ContentTypeLive),        // rule 2 keeps
		stream("Shop TV", "http://p/live/3.ts", "Shopping", types.ContentTypeLive),     // default keeps, group filter drops
		stream("Duna", "http://p/movie/4.mkv", "Movies", types.ContentTypeVOD),         // default keeps, type gate drops
		stream("Band Backup", "http://p/live/5.ts", "Regional", types.ContentTypeLive), // default keeps, pattern drops
		stream("Band", "http://p/live/6.ts", "Regional", types.ContentTypeLive),        // default keeps
	)

	cases := map[string]struct {
		kept  bool
		stage string
		rule  int
	}{
		"Globo SD":    {false, StageRule, 1},
		"Globo HD":    {true, StageRule, 2},
		"Shop TV":     {false, StageGroup, 0},
		"Duna":        {false, StageType, 0},
		"Band Backup": {false, StagePattern, 0},
		"Band":        {true, StageDefault, 0},
	}
	for name, want := range cases {
		v, ok := got[name]
		if !ok {
			t.Fatalf("no verdict for %q", name)
		}
		if v.Kept != want.kept || v.Stage != want.stage || v.Rule != want.rule {
			t.Errorf("%q: got kept=%v stage=%s rule=%d, want kept=%v stage=%s rule=%d", name, v.Kept, v.Stage, v.Rule, want.kept, want.stage, want.rule)
		}
	}
	if got["Duna"].Type != types.ContentTypeVOD || got["Duna"].Group != "Movies" {
		t.Errorf("verdict should carry the resolved type and group, got %+v", got["Duna"])
	}
}

func TestVerdictsOffByDefaultAndOnWithoutFilters(t *testing.T) {
	s := stream("Any", "http://p/live/1.ts", "G", types.ContentTypeLive)

	_, report := Apply([]*types.Stream{s}, &config.SourceConfig{}, nil, NewFilterManager(), Options{})
	if report.Verdicts != nil {
		t.Fatalf("verdicts should not be collected unless asked for")
	}

	_, report = Apply([]*types.Stream{s}, &config.SourceConfig{}, nil, NewFilterManager(), Options{Verdicts: true})
	if len(report.Verdicts) != 1 || !report.Verdicts[0].Kept || report.Verdicts[0].Stage != StageDefault {
		t.Fatalf("a source without filters keeps everything by default, got %+v", report.Verdicts)
	}
}
