// work/db/sourcegroups.go
package db

import (
	"encoding/json"
	"strings"
	"time"

	"kptv-proxy/work/logger"
)

// SourceGroup is one row of a source's group inventory: what the last import
// saw in that group and how much of it the filters kept. The filter UI offers
// these rows for selection, so an operator picks from real provider labels.
type SourceGroup struct {
	SourceURL   string   `json:"-"`
	Name        string   `json:"name"`
	ContentType string   `json:"type"`
	Total       int      `json:"total"`
	Kept        int      `json:"kept"`
	Samples     []string `json:"samples"`
}

// SourceImport is the aggregate outcome of a source's most recent import.
type SourceImport struct {
	SourceURL  string
	ImportedAt time.Time
	DurationMs int64
	Total      int
	Kept       int
	OK         bool
	Error      string
}

// ReplaceSourceGroups swaps a source's inventory for the given rows in one
// transaction, so a reader never sees a half-written list.
func ReplaceSourceGroups(sourceURL string, groups []SourceGroup) error {
	tx, err := Get().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM kp_source_groups WHERE source_url = ?`, sourceURL); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO kp_source_groups
			(source_url, group_name, content_type, stream_count, kept_count, samples, updated_at)
		VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, g := range groups {
		samples := ""
		if len(g.Samples) > 0 {
			if b, err := json.Marshal(g.Samples); err == nil {
				samples = string(b)
			}
		}
		if _, err := stmt.Exec(sourceURL, g.Name, g.ContentType, g.Total, g.Kept, samples, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetSourceGroups returns a source's inventory, largest group first. The
// result is never nil so it serialises as an empty list.
func GetSourceGroups(sourceURL string) ([]SourceGroup, error) {
	rows, err := GetReader().Query(`
		SELECT group_name, content_type, stream_count, kept_count, samples
		FROM kp_source_groups
		WHERE source_url = ?
		ORDER BY stream_count DESC, group_name ASC`, sourceURL)
	if err != nil {
		logger.Error("{db/sourcegroups - GetSourceGroups} query failed: %v", err)
		return nil, err
	}
	defer rows.Close()

	groups := make([]SourceGroup, 0)
	for rows.Next() {
		g := SourceGroup{SourceURL: sourceURL}
		var samples string
		if err := rows.Scan(&g.Name, &g.ContentType, &g.Total, &g.Kept, &samples); err != nil {
			return nil, err
		}
		if samples != "" {
			_ = json.Unmarshal([]byte(samples), &g.Samples)
		}
		if g.Samples == nil {
			g.Samples = []string{}
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// UpsertSourceImport records how a source's latest import went, replacing the
// previous record for that source.
func UpsertSourceImport(rec SourceImport) error {
	ok := 0
	if rec.OK {
		ok = 1
	}
	_, err := Get().Exec(`
		INSERT INTO kp_source_imports
			(source_url, imported_at, duration_ms, stream_count, kept_count, ok, error)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(source_url) DO UPDATE SET
			imported_at = excluded.imported_at,
			duration_ms = excluded.duration_ms,
			stream_count = excluded.stream_count,
			kept_count = excluded.kept_count,
			ok = excluded.ok,
			error = excluded.error`,
		rec.SourceURL, rec.ImportedAt.UTC().Format(time.RFC3339), rec.DurationMs,
		rec.Total, rec.Kept, ok, rec.Error)
	if err != nil {
		logger.Error("{db/sourcegroups - UpsertSourceImport} %v", err)
	}
	return err
}

// GetSourceImports returns the latest import outcome of every source, keyed
// by source URL.
func GetSourceImports() (map[string]SourceImport, error) {
	rows, err := GetReader().Query(`
		SELECT source_url, imported_at, duration_ms, stream_count, kept_count, ok, error
		FROM kp_source_imports`)
	if err != nil {
		logger.Error("{db/sourcegroups - GetSourceImports} query failed: %v", err)
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]SourceImport)
	for rows.Next() {
		var rec SourceImport
		var at string
		var ok int
		if err := rows.Scan(&rec.SourceURL, &at, &rec.DurationMs, &rec.Total, &rec.Kept, &ok, &rec.Error); err != nil {
			return nil, err
		}
		rec.ImportedAt, _ = time.Parse(time.RFC3339, at)
		rec.OK = ok == 1
		out[rec.SourceURL] = rec
	}
	return out, rows.Err()
}

// PruneSourceData drops inventory and import records for sources that are no
// longer configured, so a removed provider's groups stop appearing in the UI.
func PruneSourceData(keepURLs []string) error {
	tx, err := Get().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, table := range []string{"kp_source_groups", "kp_source_imports"} {
		if len(keepURLs) == 0 {
			if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
				return err
			}
			continue
		}
		args := make([]any, len(keepURLs))
		for i, u := range keepURLs {
			args[i] = u
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keepURLs)), ",")
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE source_url NOT IN (`+placeholders+`)`, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}
