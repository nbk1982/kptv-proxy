package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/proxy"
	"net/http"
)

// maskedSecret stands in for stored credentials on config reads. A value posted
// back unchanged means "keep what is stored".
const maskedSecret = "********"

// maskSecret replaces a non-empty stored credential with the mask sentinel.
func maskSecret(v string) string {
	if v == "" {
		return ""
	}
	return maskedSecret
}

// handleGetConfig serialises the current runtime configuration to JSON for
// the admin interface. Reads directly from the live config on the proxy
// instance rather than from disk.
func handleGetConfig(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		cfg := sp.Config

		// Marshal sources with duration fields as strings.
		type sourceOut struct {
			Name                   string              `json:"name"`
			URL                    string              `json:"url"`
			Order                  int                 `json:"order"`
			MaxConnections         int                 `json:"maxConnections"`
			MaxStreamTimeout       string              `json:"maxStreamTimeout"`
			RetryDelay             string              `json:"retryDelay"`
			MaxRetries             int                 `json:"maxRetries"`
			MaxFailuresBeforeBlock int                 `json:"maxFailuresBeforeBlock"`
			MinDataSize            int64               `json:"minDataSize"`
			UserAgent              string              `json:"userAgent"`
			ReqOrigin              string              `json:"reqOrigin"`
			ReqReferrer            string              `json:"reqReferrer"`
			Username               string              `json:"username"`
			Password               string              `json:"password"`
			LiveIncludeRegex       string              `json:"liveIncludeRegex"`
			LiveExcludeRegex       string              `json:"liveExcludeRegex"`
			SeriesIncludeRegex     string              `json:"seriesIncludeRegex"`
			SeriesExcludeRegex     string              `json:"seriesExcludeRegex"`
			VODIncludeRegex        string              `json:"vodIncludeRegex"`
			VODExcludeRegex        string              `json:"vodExcludeRegex"`
			LiveCategoryRegex      string              `json:"liveCategoryRegex"`
			VODCategoryRegex       string              `json:"vodCategoryRegex"`
			SeriesCategoryRegex    string              `json:"seriesCategoryRegex"`
			GroupFilterMode        string              `json:"groupFilterMode"`
			GroupFilterList        []string            `json:"groupFilterList"`
			GroupFilterRegex       string              `json:"groupFilterRegex"`
			ImportTypes            []string            `json:"importTypes"`
			GroupTypeOverrides     map[string]string   `json:"groupTypeOverrides"`
			FilterProfile          string              `json:"filterProfile"`
			FilterRules            []config.FilterRule `json:"filterRules"`
			FilterDefault          string              `json:"filterDefault"`
			QualityDedupe          bool                `json:"qualityDedupe"`
			QualityTiers           []string            `json:"qualityTiers"`
		}
		sources := make([]sourceOut, len(cfg.Sources))
		for i := range cfg.Sources {
			s := &cfg.Sources[i]
			sources[i] = sourceOut{
				Name: s.Name, URL: s.URL, Order: s.Order,
				MaxConnections:         s.MaxConnections,
				MaxStreamTimeout:       s.MaxStreamTimeout.String(),
				RetryDelay:             s.RetryDelay.String(),
				MaxRetries:             s.MaxRetries,
				MaxFailuresBeforeBlock: s.MaxFailuresBeforeBlock,
				MinDataSize:            s.MinDataSize, UserAgent: s.UserAgent,
				ReqOrigin: s.ReqOrigin, ReqReferrer: s.ReqReferrer,
				Username: s.Username, Password: maskSecret(s.Password),
				LiveIncludeRegex: s.LiveIncludeRegex, LiveExcludeRegex: s.LiveExcludeRegex,
				SeriesIncludeRegex: s.SeriesIncludeRegex, SeriesExcludeRegex: s.SeriesExcludeRegex,
				VODIncludeRegex: s.VODIncludeRegex, VODExcludeRegex: s.VODExcludeRegex,
				LiveCategoryRegex: s.LiveCategoryRegex, VODCategoryRegex: s.VODCategoryRegex,
				SeriesCategoryRegex: s.SeriesCategoryRegex,
				GroupFilterMode:     s.GroupFilterMode, GroupFilterList: nonNilList(s.GroupFilterList),
				GroupFilterRegex: s.GroupFilterRegex, ImportTypes: nonNilList(s.ImportTypes),
				GroupTypeOverrides: nonNilMap(s.GroupTypeOverrides),
				FilterProfile:      s.FilterProfile,
				FilterRules:        nonNilRules(s.FilterRules),
				FilterDefault:      s.FilterDefault,
				QualityDedupe:      s.QualityDedupe,
				QualityTiers:       nonNilList(s.QualityTiers),
			}
		}

		out := map[string]any{
			"baseURL":                cfg.BaseURL,
			"bufferSizePerStream":    cfg.BufferSizePerStream,
			"cacheEnabled":           cfg.CacheEnabled,
			"cacheDuration":          cfg.CacheDuration.String(),
			"importRefreshInterval":  cfg.ImportRefreshInterval.String(),
			"workerThreads":          cfg.WorkerThreads,
			"debug":                  cfg.Debug,
			"logLevel":               cfg.LogLevel,
			"obfuscateUrls":          cfg.ObfuscateUrls,
			"sortField":              cfg.SortField,
			"sortDirection":          cfg.SortDirection,
			"streamTimeout":          cfg.StreamTimeout.String(),
			"maxConnectionsToApp":    cfg.MaxConnectionsToApp,
			"watcherEnabled":         cfg.WatcherEnabled,
			"ffmpegMode":             cfg.FFmpegMode,
			"ffmpegPreInput":         cfg.FFmpegPreInput,
			"ffmpegPreOutput":        cfg.FFmpegPreOutput,
			"responseHeaderTimeout":  cfg.ResponseHeaderTimeout.String(),
			"slowClientBufferChunks": cfg.SlowClientBufferChunks,
			"tmdbEnabled":            cfg.TMDBEnabled,
			"tmdbApiKey":             maskSecret(cfg.TMDBAPIKey),
			"sources":                sources,
		}

		if err := json.NewEncoder(w).Encode(out); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to encode config: %v", err))
			http.Error(w, "Failed to encode config", http.StatusInternalServerError)
		}
	}
}

// handleSetConfig decodes a JSON config payload from the admin interface,
// validates the base URL, persists every field to SQLite via PersistConfig,
// and clears the in-memory config cache so the next LoadConfig call returns
// fresh data.
func handleSetConfig(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addLogEntry("info", "POST /api/config received")

		defer func() {
			if err := recover(); err != nil {
				addLogEntry("error", fmt.Sprintf("PANIC in handleSetConfig: %v", err))
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		}()

		w.Header().Set("Content-Type", "application/json")

		r.Body = http.MaxBytesReader(w, r.Body, constants.Internal.MaxConfigBodyBytes)

		body, err := io.ReadAll(r.Body)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to read request body: %v", err))
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}

		// Decode into the runtime Config type directly — no intermediate
		// ConfigFile needed now that persistence goes through SQLite.
		var incoming config.Config
		if err := json.Unmarshal(body, &incoming); err != nil {
			addLogEntry("error", fmt.Sprintf("JSON decode error: %v", err))
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		if incoming.BaseURL == "" {
			addLogEntry("error", "Base URL is required but empty")
			http.Error(w, "Base URL is required", http.StatusBadRequest)
			return
		}

		// Credentials are masked on read; a value posted back unchanged means
		// keep the stored secret rather than persisting the mask itself
		if incoming.TMDBAPIKey == maskedSecret {
			incoming.TMDBAPIKey = sp.Config.TMDBAPIKey
		}
		for i := range incoming.Sources {
			if incoming.Sources[i].Password == maskedSecret {
				incoming.Sources[i].Password = storedPassword(sp, &incoming.Sources[i])
			}
		}

		// Reject a filter the import could not honour, naming the field so the
		// UI can point at it; the engine would otherwise silently disable it.
		// A source whose filters are byte-identical to the stored ones is let
		// through with a warning: patterns saved before this validation existed
		// must not block saving unrelated settings.
		for i := range incoming.Sources {
			src := &incoming.Sources[i]
			err := src.NormalizeFilters()
			if err == nil {
				continue
			}
			if stored := findStoredSource(sp, src); stored != nil && config.FiltersEqual(src, stored) {
				addLogEntry("warning", fmt.Sprintf("Keeping already-stored filter that the import cannot honour: %v", err))
				continue
			}
			addLogEntry("error", fmt.Sprintf("Rejected config: %v", err))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// a source may only name a rule set that exists, or its filters would
		// silently be the ones it carries itself
		for i := range incoming.Sources {
			src := &incoming.Sources[i]
			if src.FilterProfile == "" || sp.Config.FilterProfileByName(src.FilterProfile) != nil {
				continue
			}
			msg := fmt.Sprintf("source %q names filter profile %q, which does not exist", src.Name, src.FilterProfile)
			addLogEntry("error", "Rejected config: "+msg)
			http.Error(w, msg, http.StatusBadRequest)
			return
		}

		// The UI posts the whole document but omits the shared rule sets, which
		// have their own endpoints; keep what is stored rather than dropping it
		if incoming.FilterProfiles == nil {
			incoming.FilterProfiles = sp.Config.FilterProfiles
		}

		// Ensure FFmpeg slices are never nil in the persisted config.
		if incoming.FFmpegPreInput == nil {
			incoming.FFmpegPreInput = []string{}
		}
		if incoming.FFmpegPreOutput == nil {
			incoming.FFmpegPreOutput = []string{}
		}

		// Clear compiled regex filters so changed patterns are recompiled on next use.
		if sp.FilterManager != nil {
			sp.FilterManager.ClearFilters()
			addLogEntry("info", "Cleared compiled regex filters due to config update")
		}

		if err := config.PersistConfig(&incoming); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to persist config: %v", err))
			http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Invalidate the in-memory cache so the next LoadConfig reads fresh data.
		config.ClearConfigCache()

		// Reload from SQLite and swap the live config pointer so the saved
		// settings apply immediately, not only after a graceful restart
		sp.Config = config.LoadConfig()

		// Rate limiters are built per source URL and cached, so a changed
		// max connections value would otherwise keep the old rate until the
		// process restarted — which saving a source no longer does
		sp.ReinitRateLimiters()

		addLogEntry("info", "Configuration updated via admin interface")

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}
