// work/admin/sources.go
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/db"
	"kptv-proxy/work/proxy"
)

// previewTimeout bounds one preview fetch. A large playlist can take tens of
// seconds to download from a provider; the UI shows progress meanwhile.
const previewTimeout = 2 * time.Minute

// findSource returns the configured source with the given URL, or nil.
func findSource(sp *proxy.StreamProxy, url string) *config.SourceConfig {
	for i := range sp.Config.Sources {
		if sp.Config.Sources[i].URL == url {
			return &sp.Config.Sources[i]
		}
	}
	return nil
}

// findStoredSource returns the configured source a payload refers to: the one
// with the same name and URL, falling back to the URL alone so renaming a
// source in the modal does not detach it from what is stored.
func findStoredSource(sp *proxy.StreamProxy, src *config.SourceConfig) *config.SourceConfig {
	for i := range sp.Config.Sources {
		if sp.Config.Sources[i].Name == src.Name && sp.Config.Sources[i].URL == src.URL {
			return &sp.Config.Sources[i]
		}
	}
	return findSource(sp, src.URL)
}

// storedPassword returns the credential held for a source, for a payload that
// posted the mask back instead of a new value.
func storedPassword(sp *proxy.StreamProxy, src *config.SourceConfig) string {
	if stored := findStoredSource(sp, src); stored != nil {
		return stored.Password
	}
	return ""
}

// writeJSON encodes v as the response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		addLogEntry("error", fmt.Sprintf("Failed to encode response: %v", err))
	}
}

// nonNilList returns an empty slice for nil so the field serialises as [].
func nonNilList(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// nonNilMap returns an empty map for nil so the field serialises as {}.
func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// importJSON is the wire form of a source's last import record.
func importJSON(rec db.SourceImport) map[string]any {
	return map[string]any{
		"lastImportAt": rec.ImportedAt,
		"durationMs":   rec.DurationMs,
		"total":        rec.Total,
		"kept":         rec.Kept,
		"ok":           rec.OK,
		"error":        rec.Error,
	}
}

// handleGetSourceGroups serves the group inventory recorded by a source's last
// import, so the filter UI can offer real provider labels the moment the modal
// opens, without a round trip to the provider.
func handleGetSourceGroups(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		url := r.URL.Query().Get("url")
		if url == "" {
			http.Error(w, "url is required", http.StatusBadRequest)
			return
		}
		if findSource(sp, url) == nil {
			http.Error(w, "No source with that URL", http.StatusNotFound)
			return
		}

		groups, err := db.GetSourceGroups(url)
		if err != nil {
			http.Error(w, "Failed to load groups", http.StatusInternalServerError)
			return
		}

		out := map[string]any{"url": url, "groups": groups}
		if imports, err := db.GetSourceImports(); err == nil {
			if rec, ok := imports[url]; ok {
				out["import"] = importJSON(rec)
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handlePreviewSource fetches a source with the draft configuration in the
// request body and reports what its rules would keep, without changing the
// live catalog. The source is sent in the same shape POST /api/config accepts,
// so the UI previews exactly what it is about to save.
func handlePreviewSource(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, constants.Internal.MaxConfigBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}

		var req struct {
			Source json.RawMessage `json:"source"`
			Force  bool            `json:"force"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		var src config.SourceConfig
		if err := config.ParseSourceJSON(req.Source, &src); err != nil {
			http.Error(w, "Invalid source: "+err.Error(), http.StatusBadRequest)
			return
		}
		if src.URL == "" {
			http.Error(w, "URL is required", http.StatusBadRequest)
			return
		}

		// the UI never sees the stored password; the mask means "use what is saved"
		if src.Password == maskedSecret {
			src.Password = storedPassword(sp, &src)
		}

		if err := src.NormalizeFilters(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), previewTimeout)
		defer cancel()

		started := time.Now()
		report, cached, err := sp.PreviewSource(ctx, &src, req.Force)
		if err != nil {
			// the client navigated away or the wait for another preview to
			// finish outlasted this request; there is nothing to report
			if errors.Is(err, context.Canceled) {
				return
			}
			addLogEntry("warning", fmt.Sprintf("Source preview failed for %s: %v", src.Name, err))
			status := http.StatusBadGateway
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			http.Error(w, err.Error(), status)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"cached":     cached,
			"durationMs": time.Since(started).Milliseconds(),
			"report":     report,
		})
	}
}

// handleTriggerImport starts a catalog import in the background so a saved
// filter applies without restarting the service. The optional body names one
// source to re-download; force drops its raw cache first.
func handleTriggerImport(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL   string `json:"url"`
			Force bool   `json:"force"`
		}
		if r.Body != nil {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
			if err != nil {
				http.Error(w, "Failed to read body", http.StatusBadRequest)
				return
			}
			if len(body) > 0 {
				if err := json.Unmarshal(body, &req); err != nil {
					http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
					return
				}
			}
		}
		if req.URL != "" && findSource(sp, req.URL) == nil {
			http.Error(w, "No source with that URL", http.StatusNotFound)
			return
		}

		if err := sp.TriggerImport(req.URL, req.Force); err != nil {
			if errors.Is(err, proxy.ErrImportRunning) {
				writeJSON(w, http.StatusConflict, map[string]string{"status": "running", "message": err.Error()})
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		addLogEntry("info", "Stream import triggered via admin interface")
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	}
}

// handleImportStatus reports whether an import is running and how each
// source's last import went, for the progress indicators on the source cards.
func handleImportStatus(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, sp.ImportStatus())
	}
}
