// work/admin/filterprofiles.go
package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"kptv-proxy/work/config"
	"kptv-proxy/work/db"
	"kptv-proxy/work/proxy"

	"github.com/gorilla/mux"
)

// profileOut is the wire shape of a filter profile, with the number of sources
// attached so the UI can warn before a delete and explain a rename.
type profileOut struct {
	ID      int64               `json:"id"`
	Name    string              `json:"name"`
	Default string              `json:"default"`
	Rules   []config.FilterRule `json:"rules"`
	Sources []string            `json:"sources"`
}

// profileID reads the {id} path variable.
func profileID(r *http.Request) (int64, error) {
	return strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
}

// sourcesUsingProfile lists the configured sources attached to a profile.
func sourcesUsingProfile(sp *proxy.StreamProxy, name string) []string {
	var names []string
	for i := range sp.Config.Sources {
		if sp.Config.Sources[i].FilterProfile == name {
			names = append(names, sp.Config.Sources[i].Name)
		}
	}
	if names == nil {
		names = []string{}
	}
	return names
}

// decodeProfile reads and validates a profile payload.
func decodeProfile(r *http.Request) (config.FilterProfile, error) {
	var incoming config.FilterProfile
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&incoming); err != nil {
		return incoming, fmt.Errorf("invalid JSON: %v", err)
	}
	if err := incoming.Normalize(); err != nil {
		return incoming, err
	}
	return incoming, nil
}

// reloadConfig re-reads the configuration so a profile change is live at once,
// and drops the compiled filters that were built from the previous rules.
func reloadConfig(sp *proxy.StreamProxy) {
	config.ClearConfigCache()
	sp.Config = config.LoadConfig()
	if sp.FilterManager != nil {
		sp.FilterManager.ClearFilters()
	}
}

// handleGetFilterProfiles lists every shared rule set.
func handleGetFilterProfiles(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.GetAllFilterProfiles()
		if err != nil {
			http.Error(w, "Failed to load filter profiles", http.StatusInternalServerError)
			return
		}

		out := make([]profileOut, 0, len(rows))
		for _, row := range rows {
			rules := config.DecodeRules(row.Rules)
			if rules == nil {
				rules = []config.FilterRule{}
			}
			out = append(out, profileOut{
				ID:      row.ID,
				Name:    row.Name,
				Default: row.DefaultAction,
				Rules:   rules,
				Sources: sourcesUsingProfile(sp, row.Name),
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handleCreateFilterProfile stores a new shared rule set.
func handleCreateFilterProfile(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		incoming, err := decodeProfile(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		id, err := db.InsertFilterProfile(db.FilterProfile{
			Name:          incoming.Name,
			DefaultAction: incoming.Default,
			Rules:         config.EncodeRules(incoming.Rules),
		})
		if err != nil {
			// the unique index on the name is the only expected failure here
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				http.Error(w, fmt.Sprintf("A filter profile named %q already exists", incoming.Name), http.StatusConflict)
				return
			}
			http.Error(w, "Failed to save filter profile", http.StatusInternalServerError)
			return
		}

		reloadConfig(sp)
		addLogEntry("info", fmt.Sprintf("Filter profile %q created with %d rules", incoming.Name, len(incoming.Rules)))
		writeJSON(w, http.StatusCreated, profileOut{
			ID: id, Name: incoming.Name, Default: incoming.Default,
			Rules: incoming.Rules, Sources: sourcesUsingProfile(sp, incoming.Name),
		})
	}
}

// handleUpdateFilterProfile replaces a shared rule set. A rename carries the
// sources that named it, so their filters stay attached.
func handleUpdateFilterProfile(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := profileID(r)
		if err != nil {
			http.Error(w, "Invalid profile id", http.StatusBadRequest)
			return
		}
		incoming, err := decodeProfile(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if _, err := db.GetFilterProfile(id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "No filter profile with that id", http.StatusNotFound)
				return
			}
			http.Error(w, "Failed to load filter profile", http.StatusInternalServerError)
			return
		}

		err = db.UpdateFilterProfile(db.FilterProfile{
			ID:            id,
			Name:          incoming.Name,
			DefaultAction: incoming.Default,
			Rules:         config.EncodeRules(incoming.Rules),
		})
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				http.Error(w, fmt.Sprintf("A filter profile named %q already exists", incoming.Name), http.StatusConflict)
				return
			}
			http.Error(w, "Failed to save filter profile", http.StatusInternalServerError)
			return
		}

		reloadConfig(sp)
		addLogEntry("info", fmt.Sprintf("Filter profile %q updated with %d rules", incoming.Name, len(incoming.Rules)))
		writeJSON(w, http.StatusOK, profileOut{
			ID: id, Name: incoming.Name, Default: incoming.Default,
			Rules: incoming.Rules, Sources: sourcesUsingProfile(sp, incoming.Name),
		})
	}
}

// handleDeleteFilterProfile removes a shared rule set. A profile still in use
// is refused unless force is set, in which case the sources are detached and
// fall back to their own rules alone.
func handleDeleteFilterProfile(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := profileID(r)
		if err != nil {
			http.Error(w, "Invalid profile id", http.StatusBadRequest)
			return
		}

		row, err := db.GetFilterProfile(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "No filter profile with that id", http.StatusNotFound)
				return
			}
			http.Error(w, "Failed to load filter profile", http.StatusInternalServerError)
			return
		}

		if used := sourcesUsingProfile(sp, row.Name); len(used) > 0 && r.URL.Query().Get("force") != "true" {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   fmt.Sprintf("%q is used by %d source(s)", row.Name, len(used)),
				"sources": used,
			})
			return
		}

		if err := db.DeleteFilterProfile(id); err != nil {
			http.Error(w, "Failed to delete filter profile", http.StatusInternalServerError)
			return
		}

		reloadConfig(sp)
		addLogEntry("info", fmt.Sprintf("Filter profile %q deleted", row.Name))
		writeJSON(w, http.StatusOK, map[string]string{"status": "success"})
	}
}
