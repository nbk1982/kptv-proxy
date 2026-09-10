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
	if len(src.GroupFilterList) != 2 || src.GroupFilterList[0] != "♦️  GLOBO" || src.GroupFilterList[1] != "♦️  SBT" {
		t.Fatalf("list should be trimmed and deduped by GroupKey, got %q", src.GroupFilterList)
	}
	if src.GroupFilterRegex != "^x" {
		t.Fatalf("regex should be trimmed, got %q", src.GroupFilterRegex)
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
