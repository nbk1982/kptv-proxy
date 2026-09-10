package db

import (
	"testing"
	"time"
)

// withSchema points the package-level connections at a fresh database so the
// accessors under test run against real SQLite.
func withSchema(t *testing.T) {
	t.Helper()
	d := openTemp(t, "groups.db")
	if err := initSchema(d); err != nil {
		t.Fatalf("initSchema: %v", err)
	}
	// claim the singleton before anything calls Get(), which would otherwise
	// run the real initializer and open the configured database file
	once.Do(func() {})
	prevInstance, prevReader := instance, reader
	instance, reader = d, d
	t.Cleanup(func() { instance, reader = prevInstance, prevReader })
}

func TestSourceGroupsRoundTripAndOrdering(t *testing.T) {
	withSchema(t)

	if got, err := GetSourceGroups("http://p/list.m3u"); err != nil || len(got) != 0 {
		t.Fatalf("an unknown source must read as an empty list, got %v (%v)", got, err)
	}

	err := ReplaceSourceGroups("http://p/list.m3u", []SourceGroup{
		{Name: "♦️  GLOBO", ContentType: "live", Total: 5, Kept: 2, Samples: []string{"GLOBO SP FHD", "GLOBO RJ FHD"}},
		{Name: "♦️ Novelas", ContentType: "series", Total: 90, Kept: 0},
		{Name: "", ContentType: "live", Total: 1, Kept: 1},
	})
	if err != nil {
		t.Fatalf("ReplaceSourceGroups: %v", err)
	}

	got, err := GetSourceGroups("http://p/list.m3u")
	if err != nil {
		t.Fatalf("GetSourceGroups: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d", len(got))
	}
	if got[0].Name != "♦️ Novelas" || got[0].Total != 90 || got[0].ContentType != "series" {
		t.Fatalf("largest group must come first: %+v", got[0])
	}
	if got[1].Name != "♦️  GLOBO" || got[1].Kept != 2 || len(got[1].Samples) != 2 || got[1].Samples[0] != "GLOBO SP FHD" {
		t.Fatalf("samples or counts lost: %+v", got[1])
	}
	// a group with no samples still serialises as a list rather than null
	if got[2].Name != "" || got[2].Samples == nil || len(got[2].Samples) != 0 {
		t.Fatalf("empty-label row wrong: %+v", got[2])
	}

	// replacing is a swap, not an append
	if err := ReplaceSourceGroups("http://p/list.m3u", []SourceGroup{{Name: "Only", ContentType: "live", Total: 1}}); err != nil {
		t.Fatalf("second replace: %v", err)
	}
	if got, _ := GetSourceGroups("http://p/list.m3u"); len(got) != 1 || got[0].Name != "Only" {
		t.Fatalf("replace must drop the previous rows, got %+v", got)
	}
}

func TestSourceImportRecordUpserts(t *testing.T) {
	withSchema(t)

	at := time.Now().UTC().Truncate(time.Second)
	if err := UpsertSourceImport(SourceImport{SourceURL: "http://p/a", ImportedAt: at, DurationMs: 1200, Total: 100, Kept: 10, OK: true}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := UpsertSourceImport(SourceImport{SourceURL: "http://p/a", ImportedAt: at.Add(time.Minute), Total: 100, OK: false, Error: "fetch timed out"}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	imports, err := GetSourceImports()
	if err != nil {
		t.Fatalf("GetSourceImports: %v", err)
	}
	if len(imports) != 1 {
		t.Fatalf("the same source must hold one record, got %d", len(imports))
	}
	rec := imports["http://p/a"]
	if rec.OK || rec.Error != "fetch timed out" || rec.Kept != 0 {
		t.Fatalf("the later record must win: %+v", rec)
	}
	if !rec.ImportedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("timestamp lost in the round trip: %v vs %v", rec.ImportedAt, at.Add(time.Minute))
	}
}

func TestPruneSourceDataDropsUnconfiguredSources(t *testing.T) {
	withSchema(t)

	for _, url := range []string{"http://p/a", "http://p/b"} {
		if err := ReplaceSourceGroups(url, []SourceGroup{{Name: "g", ContentType: "live", Total: 1}}); err != nil {
			t.Fatal(err)
		}
		if err := UpsertSourceImport(SourceImport{SourceURL: url, ImportedAt: time.Now(), OK: true}); err != nil {
			t.Fatal(err)
		}
	}

	if err := PruneSourceData([]string{"http://p/a"}); err != nil {
		t.Fatalf("PruneSourceData: %v", err)
	}
	if got, _ := GetSourceGroups("http://p/b"); len(got) != 0 {
		t.Fatalf("the removed source's groups must go, got %+v", got)
	}
	if got, _ := GetSourceGroups("http://p/a"); len(got) != 1 {
		t.Fatal("the configured source's groups must stay")
	}
	imports, _ := GetSourceImports()
	if _, exists := imports["http://p/b"]; exists {
		t.Fatal("the removed source's import record must go")
	}
	if _, exists := imports["http://p/a"]; !exists {
		t.Fatal("the configured source's import record must stay")
	}

	// no sources configured at all clears both tables
	if err := PruneSourceData(nil); err != nil {
		t.Fatalf("PruneSourceData(nil): %v", err)
	}
	if got, _ := GetSourceGroups("http://p/a"); len(got) != 0 {
		t.Fatal("pruning with no sources must clear everything")
	}
	if imports, _ := GetSourceImports(); len(imports) != 0 {
		t.Fatal("pruning with no sources must clear the import records too")
	}
}
