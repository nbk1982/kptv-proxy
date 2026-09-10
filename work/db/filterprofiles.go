// work/db/filterprofiles.go
package db

import (
	"database/sql"
	"time"

	"kptv-proxy/work/logger"
)

// FilterProfile mirrors a kp_filter_profiles row. Rules travel as the JSON
// text the config package encodes.
type FilterProfile struct {
	ID            int64
	Name          string
	DefaultAction string
	Rules         string
}

// GetAllFilterProfiles returns every profile ordered by name.
func GetAllFilterProfiles() ([]FilterProfile, error) {
	rows, err := GetReader().Query(`
		SELECT id, name, default_action, rules
		FROM kp_filter_profiles
		ORDER BY name ASC`)
	if err != nil {
		logger.Error("{db/filterprofiles - GetAllFilterProfiles} query failed: %v", err)
		return nil, err
	}
	defer rows.Close()

	profiles := make([]FilterProfile, 0)
	for rows.Next() {
		var p FilterProfile
		if err := rows.Scan(&p.ID, &p.Name, &p.DefaultAction, &p.Rules); err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

// GetFilterProfile returns one profile by ID, or sql.ErrNoRows.
func GetFilterProfile(id int64) (FilterProfile, error) {
	var p FilterProfile
	err := GetReader().QueryRow(`
		SELECT id, name, default_action, rules
		FROM kp_filter_profiles WHERE id = ?`, id).Scan(&p.ID, &p.Name, &p.DefaultAction, &p.Rules)
	return p, err
}

// InsertFilterProfile stores a new profile and returns its ID. A duplicate
// name violates the unique index and comes back as an error.
func InsertFilterProfile(p FilterProfile) (int64, error) {
	res, err := Get().Exec(`
		INSERT INTO kp_filter_profiles (name, default_action, rules, updated_at)
		VALUES (?,?,?,?)`,
		p.Name, p.DefaultAction, p.Rules, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		logger.Error("{db/filterprofiles - InsertFilterProfile} %v", err)
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateFilterProfile replaces a profile and re-points every source that named
// it, so renaming one keeps the sources attached. Both happen in one
// transaction: a rename that lost its references would silently unfilter a
// source's catalog.
func UpdateFilterProfile(p FilterProfile) error {
	tx, err := Get().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var previous string
	if err := tx.QueryRow(`SELECT name FROM kp_filter_profiles WHERE id = ?`, p.ID).Scan(&previous); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE kp_filter_profiles SET name=?, default_action=?, rules=?, updated_at=?
		WHERE id=?`,
		p.Name, p.DefaultAction, p.Rules, time.Now().UTC().Format(time.RFC3339), p.ID); err != nil {
		return err
	}
	if previous != p.Name {
		if _, err := tx.Exec(`UPDATE kp_sources SET filter_profile=? WHERE filter_profile=?`, p.Name, previous); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteFilterProfile removes a profile and detaches the sources that named
// it, which then fall back to their own rules alone.
func DeleteFilterProfile(id int64) error {
	tx, err := Get().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var name string
	if err := tx.QueryRow(`SELECT name FROM kp_filter_profiles WHERE id = ?`, id).Scan(&name); err != nil {
		if err == sql.ErrNoRows {
			return err
		}
		return err
	}
	if _, err := tx.Exec(`DELETE FROM kp_filter_profiles WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE kp_sources SET filter_profile='' WHERE filter_profile=?`, name); err != nil {
		return err
	}
	return tx.Commit()
}

// FilterProfileUsage counts the sources attached to each profile name.
func FilterProfileUsage() (map[string]int, error) {
	rows, err := GetReader().Query(`
		SELECT filter_profile, count(*) FROM kp_sources
		WHERE filter_profile != '' GROUP BY filter_profile`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	usage := make(map[string]int)
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return nil, err
		}
		usage[name] = count
	}
	return usage, rows.Err()
}
