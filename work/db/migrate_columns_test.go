// work/db/migrate_columns_test.go
package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// oldSchema is the kp_sources DDL exactly as it shipped before the category
// regex columns existed: 21 columns, ending at vod_exc_regex.
const oldSchema = `
CREATE TABLE kp_sources (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	name             TEXT    NOT NULL,
	uri              TEXT    NOT NULL,
	uname            TEXT    NOT NULL DEFAULT '',
	pword            TEXT    NOT NULL DEFAULT '',
	sort_order       INTEGER NOT NULL DEFAULT 1,
	max_cnx          INTEGER NOT NULL DEFAULT 5,
	max_stream_to    TEXT    NOT NULL DEFAULT '30s',
	retry_delay      TEXT    NOT NULL DEFAULT '5s',
	max_retries      INTEGER NOT NULL DEFAULT 3,
	max_failures     INTEGER NOT NULL DEFAULT 5,
	min_data_size    INTEGER NOT NULL DEFAULT 2,
	user_agent       TEXT    NOT NULL DEFAULT '',
	req_origin       TEXT    NOT NULL DEFAULT '',
	req_referer      TEXT    NOT NULL DEFAULT '',
	live_inc_regex   TEXT    NOT NULL DEFAULT '',
	live_exc_regex   TEXT    NOT NULL DEFAULT '',
	series_inc_regex TEXT    NOT NULL DEFAULT '',
	series_exc_regex TEXT    NOT NULL DEFAULT '',
	vod_inc_regex    TEXT    NOT NULL DEFAULT '',
	vod_exc_regex    TEXT    NOT NULL DEFAULT ''
);`

// selectList is the column list GetAllSources uses, kept in sync by hand here
// so a DDL/query name mismatch fails loudly.
const selectList = `
	SELECT id, name, uri, uname, pword, sort_order, max_cnx,
	       max_stream_to, retry_delay, max_retries, max_failures,
	       min_data_size, user_agent, req_origin, req_referer,
	       live_inc_regex, live_exc_regex, series_inc_regex,
	       series_exc_regex, vod_inc_regex, vod_exc_regex,
	       live_cat_regex, vod_cat_regex, series_cat_regex,
	       group_filter_mode, group_filter_list, group_filter_regex,
	       import_types, group_type_overrides
	FROM kp_sources ORDER BY sort_order ASC`

func openTemp(t *testing.T, name string) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })
	return d
}

func TestMigrateSourceColumnsUpgradesLegacyDatabase(t *testing.T) {
	d := openTemp(t, "legacy.db")

	if _, err := d.Exec(oldSchema); err != nil {
		t.Fatalf("old schema: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO kp_sources (name, uri, live_inc_regex) VALUES ('legacy', 'http://x/y.m3u8', '.*hbo.*')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := migrateSourceColumns(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cols, err := tableColumns(d, "kp_sources")
	if err != nil {
		t.Fatalf("tableColumns: %v", err)
	}
	for _, want := range []string{"live_cat_regex", "vod_cat_regex", "series_cat_regex", "group_filter_mode", "group_filter_list", "group_filter_regex", "import_types", "group_type_overrides"} {
		if !cols[want] {
			t.Fatalf("column %s missing after migration", want)
		}
	}

	// the pre-existing row must survive, and the new columns must backfill to ''
	rows, err := d.Query(selectList)
	if err != nil {
		t.Fatalf("select with new column list: %v", err)
	}
	defer rows.Close()
	got, err := scanSources(rows)
	if err != nil {
		t.Fatalf("scanSources: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d", len(got))
	}
	if got[0].Name != "legacy" || got[0].LiveIncRegex != ".*hbo.*" {
		t.Fatalf("pre-existing data lost: %+v", got[0])
	}
	if got[0].LiveCatRegex != "" || got[0].VODCatRegex != "" || got[0].SeriesCatRegex != "" ||
		got[0].GroupFilterMode != "" || got[0].GroupFilterList != "" || got[0].GroupFilterRegex != "" ||
		got[0].ImportTypes != "" || got[0].GroupTypeOverrides != "" {
		t.Fatalf("new columns should backfill empty, got %+v", got[0])
	}

	// running it again must be a no-op, not an error
	if err := migrateSourceColumns(d); err != nil {
		t.Fatalf("second migrate not idempotent: %v", err)
	}
}

func TestInitSchemaThenMigrateIsNoop(t *testing.T) {
	d := openTemp(t, "fresh.db")

	if err := initSchema(d); err != nil {
		t.Fatalf("initSchema: %v", err)
	}
	cols, err := tableColumns(d, "kp_sources")
	if err != nil {
		t.Fatalf("tableColumns: %v", err)
	}
	if len(cols) != 29 {
		t.Fatalf("fresh kp_sources should have 29 columns, got %d: %v", len(cols), cols)
	}
	if err := migrateSourceColumns(d); err != nil {
		t.Fatalf("migrate on fresh schema: %v", err)
	}
}
