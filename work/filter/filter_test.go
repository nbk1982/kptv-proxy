package filter

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/types"
	"testing"
)

func stream(name, url, group string, stamped types.ContentType) *types.Stream {
	return &types.Stream{
		Name:        name,
		URL:         url,
		Attributes:  map[string]string{"group-title": group},
		ContentType: stamped,
	}
}

func run(t *testing.T, src *config.SourceConfig, in ...*types.Stream) []*types.Stream {
	t.Helper()
	return FilterStreams(in, src, nil, NewFilterManager())
}

func TestCategoryRegexOverridesImporterStamp(t *testing.T) {
	// the importer stamped this as live; the VOD category pattern must win
	src := &config.SourceConfig{VODCategoryRegex: "^filmes"}
	s := stream("Filmes | Duna", "http://p/live/1.ts", "Filmes HD", types.ContentTypeLive)

	out := run(t, src, s)
	if len(out) != 1 {
		t.Fatalf("stream should be kept, got %d", len(out))
	}
	if s.ContentType != types.ContentTypeVOD {
		t.Fatalf("want vod, got %q", s.ContentType)
	}
}

func TestCategoryRegexMatchesGroupAndURLNotOnlyName(t *testing.T) {
	// nothing in the name says series; only the group does
	byGroup := stream("Breaking Bad S01E01", "http://p/x/1.mp4", "SERIES | DRAMA", types.ContentTypeLive)
	if got := run(t, &config.SourceConfig{SeriesCategoryRegex: "^series"}, byGroup); len(got) != 1 || byGroup.ContentType != types.ContentTypeSeries {
		t.Fatalf("group subject not matched: type=%q kept=%d", byGroup.ContentType, len(got))
	}

	// nothing in name or group says vod; only the URL path does
	byURL := stream("Duna", "http://p/movie/9.mkv", "Destaques", types.ContentTypeLive)
	if got := run(t, &config.SourceConfig{VODCategoryRegex: "/movie/"}, byURL); len(got) != 1 || byURL.ContentType != types.ContentTypeVOD {
		t.Fatalf("url subject not matched: type=%q kept=%d", byURL.ContentType, len(got))
	}
}

func TestAnchorsBindToOneFieldNotAConcatenation(t *testing.T) {
	// ^globo must match the NAME's start; it must not be defeated by the group
	// preceding it, nor satisfied by "globo" appearing mid-URL
	s := stream("Globo SP HD", "http://p/live/globo.ts", "Canais | Abertos", types.ContentTypeLive)
	if got := run(t, &config.SourceConfig{LiveIncludeRegex: "^globo"}, s); len(got) != 1 {
		t.Fatalf("anchored pattern should match the name field alone")
	}

	// and an anchor that matches no single field must not match
	other := stream("SBT HD", "http://p/live/sbt.ts", "Canais | Abertos", types.ContentTypeLive)
	if got := run(t, &config.SourceConfig{LiveIncludeRegex: "^globo"}, other); len(got) != 0 {
		t.Fatalf("anchored pattern should not match, kept %d", len(got))
	}
}

func TestSeriesBeatsVODBeatsLive(t *testing.T) {
	src := &config.SourceConfig{
		LiveCategoryRegex:   "hd",
		VODCategoryRegex:    "hd",
		SeriesCategoryRegex: "hd",
	}
	s := stream("Coisa HD", "http://p/a/1.ts", "grupo", types.ContentTypeLive)
	run(t, src, s)
	if s.ContentType != types.ContentTypeSeries {
		t.Fatalf("series must win, got %q", s.ContentType)
	}

	src = &config.SourceConfig{LiveCategoryRegex: "hd", VODCategoryRegex: "hd"}
	s = stream("Coisa HD", "http://p/a/1.ts", "grupo", types.ContentTypeLive)
	run(t, src, s)
	if s.ContentType != types.ContentTypeVOD {
		t.Fatalf("vod must beat live, got %q", s.ContentType)
	}
}

func TestCategoryOnlyConfigStillClassifies(t *testing.T) {
	// no include/exclude at all — the early return must not skip classification
	src := &config.SourceConfig{SeriesCategoryRegex: "s[0-9]{2}e[0-9]{2}"}
	s := stream("Dark S02E05", "http://p/a/1.mkv", "grupo", types.ContentTypeLive)

	if got := run(t, src, s); len(got) != 1 {
		t.Fatalf("nothing should be dropped, kept %d", len(got))
	}
	if s.ContentType != types.ContentTypeSeries {
		t.Fatalf("category-only source was not reclassified, got %q", s.ContentType)
	}
}

func TestUnmatchedStreamKeepsImporterClassification(t *testing.T) {
	src := &config.SourceConfig{VODCategoryRegex: "^filmes"}
	s := stream("Globo SP", "http://p/live/1.ts", "Canais", types.ContentTypeLive)
	run(t, src, s)
	if s.ContentType != types.ContentTypeLive {
		t.Fatalf("want the stamped live type preserved, got %q", s.ContentType)
	}

	s2 := stream("Dark S01E01", "http://p/series/1.mkv", "Series", types.ContentTypeSeries)
	run(t, src, s2)
	if s2.ContentType != types.ContentTypeSeries {
		t.Fatalf("want the stamped series type preserved, got %q", s2.ContentType)
	}
}

func TestIncludeExcludeApplyToTheNewlyDecidedType(t *testing.T) {
	// reclassified to vod, then dropped by the VOD exclude — proving the
	// include/exclude pair is picked from the category the regex decided
	src := &config.SourceConfig{
		VODCategoryRegex: "^filmes",
		VODExcludeRegex:  "adulto",
		LiveExcludeRegex: "nada",
	}
	dropped := stream("Filmes | Adulto XXX", "http://p/a/1.mkv", "Filmes", types.ContentTypeLive)
	if got := run(t, src, dropped); len(got) != 0 {
		t.Fatalf("should have been excluded as vod, kept %d", len(got))
	}

	kept := stream("Filmes | Duna", "http://p/a/2.mkv", "Filmes", types.ContentTypeLive)
	if got := run(t, src, kept); len(got) != 1 {
		t.Fatalf("should have been kept, kept %d", len(got))
	}
}

func TestNoPatternsConfiguredLeavesStreamsUntouched(t *testing.T) {
	src := &config.SourceConfig{}
	s := stream("Globo SP", "http://p/live/1.ts", "Canais", types.ContentTypeLive)
	out := run(t, src, s)
	if len(out) != 1 || s.ContentType != types.ContentTypeLive {
		t.Fatalf("unfiltered source must pass through unchanged")
	}
}

func TestInvalidPatternDisablesOnlyThatFilter(t *testing.T) {
	// an unparsable pattern must not take the source offline
	src := &config.SourceConfig{VODCategoryRegex: "([unclosed", SeriesCategoryRegex: "s[0-9]{2}e[0-9]{2}"}
	s := stream("Dark S02E05", "http://p/a/1.mkv", "grupo", types.ContentTypeLive)
	if got := run(t, src, s); len(got) != 1 {
		t.Fatalf("stream should survive an invalid sibling pattern, kept %d", len(got))
	}
	if s.ContentType != types.ContentTypeSeries {
		t.Fatalf("valid sibling pattern should still apply, got %q", s.ContentType)
	}
}

func TestPatternEditInvalidatesTheCachedFilter(t *testing.T) {
	fm := NewFilterManager()
	src := &config.SourceConfig{URL: "http://p/list.m3u8", VODCategoryRegex: "^filmes"}

	s := stream("Filmes | Duna", "http://p/a/1.mkv", "Filmes", types.ContentTypeLive)
	FilterStreams([]*types.Stream{s}, src, nil, fm)
	if s.ContentType != types.ContentTypeVOD {
		t.Fatalf("setup: want vod, got %q", s.ContentType)
	}

	// same URL (the cache key), different pattern — must recompile, not reuse
	src.VODCategoryRegex = ""
	src.SeriesCategoryRegex = "^filmes"
	s2 := stream("Filmes | Duna", "http://p/a/1.mkv", "Filmes", types.ContentTypeLive)
	FilterStreams([]*types.Stream{s2}, src, nil, fm)
	if s2.ContentType != types.ContentTypeSeries {
		t.Fatalf("stale cached filter served: want series, got %q", s2.ContentType)
	}
}
