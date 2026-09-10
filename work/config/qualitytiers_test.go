package config

import (
	"strings"
	"testing"
)

func TestNormalizeFiltersTidiesAndValidatesQualityTiers(t *testing.T) {
	src := &SourceConfig{Name: "p", QualityDedupe: true, QualityTiers: []string{" fhd ", "", "hd|720p"}}
	if err := src.NormalizeFilters(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(src.QualityTiers) != 2 || src.QualityTiers[0] != "fhd" || src.QualityTiers[1] != "hd|720p" {
		t.Fatalf("tiers not tidied: %q", src.QualityTiers)
	}

	// an all-blank list means the defaults, stored as nothing
	src = &SourceConfig{Name: "p", QualityDedupe: true, QualityTiers: []string{"", "  "}}
	if err := src.NormalizeFilters(); err != nil || src.QualityTiers != nil {
		t.Fatalf("blank tiers should normalize to nil, got %q err=%v", src.QualityTiers, err)
	}

	bad := &SourceConfig{Name: "p", QualityDedupe: true, QualityTiers: []string{"fhd", "hd("}}
	err := bad.NormalizeFilters()
	if err == nil || !strings.Contains(err.Error(), "quality tier") {
		t.Fatalf("an invalid tier must be rejected by name, got %v", err)
	}
}

func TestQualityDedupeCountsAsAContentFilter(t *testing.T) {
	if (&SourceConfig{}).HasContentFilters() {
		t.Fatalf("an empty source has no filters")
	}
	if !(&SourceConfig{QualityDedupe: true}).HasContentFilters() {
		t.Fatalf("the quality stage must make the filter pass run")
	}
	a, b := &SourceConfig{QualityDedupe: true}, &SourceConfig{QualityDedupe: true, QualityTiers: []string{"fhd"}}
	if FiltersEqual(a, b) || !FiltersEqual(a, &SourceConfig{QualityDedupe: true}) {
		t.Fatalf("FiltersEqual must see the quality settings")
	}
}
