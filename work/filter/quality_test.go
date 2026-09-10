package filter

import (
	"testing"

	"kptv-proxy/work/config"
	"kptv-proxy/work/types"
)

func live(name string) *types.Stream {
	return stream(name, "http://p/live/"+name+".ts", "Canais", types.ContentTypeLive)
}

func TestQualityDedupeKeepsOnlyTheBestVariant(t *testing.T) {
	src := &config.SourceConfig{QualityDedupe: true}

	out := run(t, src,
		live("A&E FHD"), live("A&E HD"), live("A&E SD"), // three qualities: FHD wins
		live("Globo HD"),                // HD only: untouched
		live("HBO 4K"), live("HBO FHD"), // 4K outranks FHD
		live("[FHD] Band"), live("Band HD"), // marker position and brackets do not matter
		live("Discovery HDR"), live("Discovery FHD"), // "hd" must not match inside "hdr"
	)

	assertNames(t, out, "A&E FHD", "Globo HD", "HBO 4K", "[FHD] Band", "Discovery HDR", "Discovery FHD")
}

func TestQualityDedupeOnlyDropsWhenTheBetterVariantIsKept(t *testing.T) {
	// the FHD feed is dropped by a rule, so the HD one has to stay
	src := &config.SourceConfig{
		QualityDedupe: true,
		FilterRules:   []config.FilterRule{{Field: "name", Action: "exclude", Pattern: "fhd$"}},
	}
	got := verdicts(t, src, live("A&E FHD"), live("A&E HD"))

	if got["A&E FHD"].Kept || got["A&E FHD"].Stage != StageRule {
		t.Fatalf("FHD should be dropped by the rule, got %+v", got["A&E FHD"])
	}
	if !got["A&E HD"].Kept || got["A&E HD"].Stage != StageDefault {
		t.Fatalf("HD must survive when no better variant is kept, got %+v", got["A&E HD"])
	}
}

func TestQualityDedupeReportsStageAndCount(t *testing.T) {
	src := &config.SourceConfig{QualityDedupe: true}
	if err := src.NormalizeFilters(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	_, report := Apply([]*types.Stream{live("A&E FHD"), live("A&E HD"), live("A&E SD")}, src, nil, NewFilterManager(), Options{Verdicts: true})

	if report.QualityDropped != 2 || report.Kept != 1 || report.Total != 3 {
		t.Fatalf("report: dropped=%d kept=%d total=%d", report.QualityDropped, report.Kept, report.Total)
	}
	for _, v := range report.Verdicts {
		if v.Name != "A&E FHD" && (v.Kept || v.Stage != StageQuality) {
			t.Fatalf("%q should be dropped at the quality stage, got %+v", v.Name, v)
		}
	}
}

func TestQualityDedupeSeparatesContentTypesAndHonoursCustomTiers(t *testing.T) {
	// a film and a channel sharing a name never compete
	src := &config.SourceConfig{QualityDedupe: true}
	film := stream("Duna FHD", "http://p/movie/1.mkv", "Filmes", types.ContentTypeVOD)
	channel := live("Duna HD")
	assertNames(t, run(t, src, film, channel), "Duna FHD", "Duna HD")

	// with tiers that do not know 4K, a 4K feed is not a variant of anything
	custom := &config.SourceConfig{QualityDedupe: true, QualityTiers: []string{"fhd", "hd"}}
	assertNames(t, run(t, custom, live("HBO 4K"), live("HBO FHD"), live("HBO HD")), "HBO 4K", "HBO FHD")
}

func TestQualityDedupeOffLeavesVariantsAlone(t *testing.T) {
	out := run(t, &config.SourceConfig{FilterDefault: "keep"}, live("A&E FHD"), live("A&E HD"))
	assertNames(t, out, "A&E FHD", "A&E HD")
}
