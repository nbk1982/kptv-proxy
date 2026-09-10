package config

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/db"
	"kptv-proxy/work/logger"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// EPGConfig represents an Electronic Program Guide source.
type EPGConfig struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Order int    `json:"order"`
}

// SDAccount represents a Schedules Direct account for EPG data retrieval.
type SDAccount struct {
	Name            string   `json:"name"`
	Username        string   `json:"username"`
	Password        string   `json:"password"`
	Enabled         bool     `json:"enabled"`
	SelectedLineups []string `json:"selectedLineups,omitempty"`
	DaysToFetch     int      `json:"daysToFetch,omitempty"`
}

// Config holds all application configuration values for the IPTV proxy server.
// All persistence is handled via SQLite through LoadConfig and PersistConfig.
type Config struct {
	BaseURL                string            `json:"baseURL"`
	BufferSizePerStream    int64             `json:"bufferSizePerStream"`
	CacheEnabled           bool              `json:"cacheEnabled"`
	CacheDuration          time.Duration     `json:"cacheDuration"`
	ImportRefreshInterval  time.Duration     `json:"importRefreshInterval"`
	WorkerThreads          int               `json:"workerThreads"`
	Debug                  bool              `json:"debug"`
	LogLevel               string            `json:"logLevel"`
	ObfuscateUrls          bool              `json:"obfuscateUrls"`
	SortField              string            `json:"sortField"`
	SortDirection          string            `json:"sortDirection"`
	StreamTimeout          time.Duration     `json:"streamTimeout"`
	MaxConnectionsToApp    int               `json:"maxConnectionsToApp"`
	Sources                []SourceConfig    `json:"sources"`
	FilterProfiles         []FilterProfile   `json:"filterProfiles,omitempty"` // reusable rule sets sources can share
	EPGs                   []EPGConfig       `json:"epgs"`
	XCOutputAccounts       []XCOutputAccount `json:"xcOutputAccounts"`
	SDAccounts             []SDAccount       `json:"sdAccounts,omitempty"`
	WatcherEnabled         bool              `json:"watcherEnabled"`
	FFmpegMode             bool              `json:"ffmpegMode"`
	FFmpegPreInput         []string          `json:"ffmpegPreInput"`
	FFmpegPreOutput        []string          `json:"ffmpegPreOutput"`
	ResponseHeaderTimeout  time.Duration     `json:"responseHeaderTimeout"`
	SlowClientBufferChunks int               `json:"slowClientBufferChunks"` // Number of chunks to queue per client before considering them too slow
	TMDBEnabled            bool              `json:"tmdbEnabled"`
	TMDBAPIKey             string            `json:"tmdbApiKey"`
}

// SourceConfig represents the configuration for a single stream source.
// ActiveConns and EPGURL are runtime-only and never persisted.
type SourceConfig struct {
	Name                   string            `json:"name"`
	URL                    string            `json:"url"`
	Order                  int               `json:"order"`
	MaxConnections         int               `json:"maxConnections"`
	MaxStreamTimeout       time.Duration     `json:"maxStreamTimeout"`
	RetryDelay             time.Duration     `json:"retryDelay"`
	MaxRetries             int               `json:"maxRetries"`
	MaxFailuresBeforeBlock int               `json:"maxFailuresBeforeBlock"`
	MinDataSize            int64             `json:"minDataSize"`
	UserAgent              string            `json:"userAgent"`
	ReqOrigin              string            `json:"reqOrigin"`
	ReqReferrer            string            `json:"reqReferrer"`
	ActiveConns            atomic.Int32      `json:"-"`
	Username               string            `json:"username"`
	Password               string            `json:"password"`
	LiveIncludeRegex       string            `json:"liveIncludeRegex,omitempty"`
	LiveExcludeRegex       string            `json:"liveExcludeRegex,omitempty"`
	SeriesIncludeRegex     string            `json:"seriesIncludeRegex,omitempty"`
	SeriesExcludeRegex     string            `json:"seriesExcludeRegex,omitempty"`
	VODIncludeRegex        string            `json:"vodIncludeRegex,omitempty"`
	VODExcludeRegex        string            `json:"vodExcludeRegex,omitempty"`
	LiveCategoryRegex      string            `json:"liveCategoryRegex,omitempty"`
	VODCategoryRegex       string            `json:"vodCategoryRegex,omitempty"`
	SeriesCategoryRegex    string            `json:"seriesCategoryRegex,omitempty"`
	GroupFilterMode        string            `json:"groupFilterMode,omitempty"`    // GroupFilterOff, GroupFilterInclude or GroupFilterExclude
	GroupFilterList        []string          `json:"groupFilterList,omitempty"`    // group labels the mode applies to, as the provider spells them
	GroupFilterRegex       string            `json:"groupFilterRegex,omitempty"`   // pattern on the group label, ORed with the list
	ImportTypes            []string          `json:"importTypes,omitempty"`        // content types to import; empty means all
	GroupTypeOverrides     map[string]string `json:"groupTypeOverrides,omitempty"` // group label -> content type forced for that group
	FilterProfile          string            `json:"filterProfile,omitempty"`      // name of a shared rule set applied before this source's own rules
	FilterRules            []FilterRule      `json:"filterRules,omitempty"`        // ordered include/exclude rules; first match decides
	FilterDefault          string            `json:"filterDefault,omitempty"`      // verdict for a stream no rule matched: keep (default) or drop
	QualityDedupe          bool              `json:"qualityDedupe,omitempty"`      // keep only the best quality variant of a channel (drop "X HD" when "X FHD" is kept)
	QualityTiers           []string          `json:"qualityTiers,omitempty"`       // quality markers best first, one pattern per tier; empty means DefaultQualityTiers
	EPGURL                 string            `json:"-"`
}

// XCOutputAccount represents an Xtream Codes compatible output account.
// ActiveConns is runtime-only and never persisted.
type XCOutputAccount struct {
	Name           string       `json:"name"`
	Username       string       `json:"username"`
	Password       string       `json:"password"`
	MaxConnections int          `json:"maxConnections"`
	EnableLive     bool         `json:"enableLive"`
	EnableSeries   bool         `json:"enableSeries"`
	EnableVOD      bool         `json:"enableVOD"`
	ActiveConns    atomic.Int32 `json:"-"`
}

var (
	configCache *Config
	configMutex sync.RWMutex
)

// UnmarshalJSON implements custom JSON unmarshaling for Config,
// handling duration fields that arrive as strings from the admin API.
func (c *Config) UnmarshalJSON(data []byte) error {
	aux := &struct {
		BaseURL                string        `json:"baseURL"`
		BufferSizePerStream    int64         `json:"bufferSizePerStream"`
		CacheEnabled           bool          `json:"cacheEnabled"`
		CacheDuration          string        `json:"cacheDuration"`
		ImportRefreshInterval  string        `json:"importRefreshInterval"`
		WorkerThreads          int           `json:"workerThreads"`
		Debug                  bool          `json:"debug"`
		LogLevel               string        `json:"logLevel"`
		ObfuscateUrls          bool          `json:"obfuscateUrls"`
		SortField              string        `json:"sortField"`
		SortDirection          string        `json:"sortDirection"`
		StreamTimeout          string        `json:"streamTimeout"`
		MaxConnectionsToApp    int           `json:"maxConnectionsToApp"`
		WatcherEnabled         bool          `json:"watcherEnabled"`
		FFmpegMode             bool          `json:"ffmpegMode"`
		FFmpegPreInput         []string      `json:"ffmpegPreInput"`
		FFmpegPreOutput        []string      `json:"ffmpegPreOutput"`
		ResponseHeaderTimeout  string        `json:"responseHeaderTimeout"`
		Sources                []sourceAlias `json:"sources"`
		SlowClientBufferChunks int           `json:"slowClientBufferChunks"`
		TMDBEnabled            bool          `json:"tmdbEnabled"`
		TMDBAPIKey             string        `json:"tmdbApiKey"`
	}{}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	c.BaseURL = aux.BaseURL
	c.BufferSizePerStream = aux.BufferSizePerStream
	c.CacheEnabled = aux.CacheEnabled
	c.WorkerThreads = aux.WorkerThreads
	c.Debug = aux.Debug
	c.LogLevel = aux.LogLevel
	c.ObfuscateUrls = aux.ObfuscateUrls
	c.SortField = aux.SortField
	c.SortDirection = aux.SortDirection
	c.MaxConnectionsToApp = aux.MaxConnectionsToApp
	c.WatcherEnabled = aux.WatcherEnabled
	c.FFmpegMode = aux.FFmpegMode
	c.FFmpegPreInput = aux.FFmpegPreInput
	c.FFmpegPreOutput = aux.FFmpegPreOutput
	c.SlowClientBufferChunks = aux.SlowClientBufferChunks
	c.TMDBEnabled = aux.TMDBEnabled
	c.TMDBAPIKey = aux.TMDBAPIKey

	var err error
	if aux.CacheDuration != "" {
		if c.CacheDuration, err = time.ParseDuration(aux.CacheDuration); err != nil {
			return fmt.Errorf("invalid cacheDuration: %w", err)
		}
	}
	if aux.ImportRefreshInterval != "" {
		if c.ImportRefreshInterval, err = time.ParseDuration(aux.ImportRefreshInterval); err != nil {
			return fmt.Errorf("invalid importRefreshInterval: %w", err)
		}
	}
	if aux.StreamTimeout != "" {
		if c.StreamTimeout, err = time.ParseDuration(aux.StreamTimeout); err != nil {
			return fmt.Errorf("invalid streamTimeout: %w", err)
		}
	}
	if aux.ResponseHeaderTimeout != "" {
		if c.ResponseHeaderTimeout, err = time.ParseDuration(aux.ResponseHeaderTimeout); err != nil {
			return fmt.Errorf("invalid responseHeaderTimeout: %w", err)
		}
	}

	c.Sources = make([]SourceConfig, len(aux.Sources))
	for i, s := range aux.Sources {
		if err := s.fill(&c.Sources[i]); err != nil {
			return err
		}
	}

	return nil
}

// LoadConfig loads configuration from SQLite, returning a cached instance on
// subsequent calls. Falls back to compiled defaults on first boot before any
// settings have been persisted.
func LoadConfig() *Config {
	configMutex.RLock()
	if configCache != nil {
		defer configMutex.RUnlock()
		return configCache
	}
	configMutex.RUnlock()

	configMutex.Lock()
	defer configMutex.Unlock()

	if configCache != nil {
		return configCache
	}

	config, err := loadFromDB()
	if err != nil {
		logger.Error("{config - LoadConfig} Failed to load config from DB: %v", err)
		config = getDefaultConfig()
	}

	validateAndSetDefaults(config)
	configCache = config
	logger.Debug("{config - LoadConfig} Configuration loaded from SQLite")
	return config
}

// ClearConfigCache resets the in-memory cache, forcing a full reload from
// SQLite on the next LoadConfig call. Called before graceful restarts.
func ClearConfigCache() {
	configMutex.Lock()
	defer configMutex.Unlock()
	configCache = nil
	logger.Debug("{config - ClearConfigCache} Config cache cleared")
}

// loadFromDB reads kp_settings and the four related tables to build a Config.
// Missing keys fall back to the compiled defaults from getDefaultConfig.
func loadFromDB() (*Config, error) {
	settings, err := db.AllSettings()
	if err != nil {
		return nil, err
	}

	cfg := getDefaultConfig()

	if v, ok := settings["baseURL"]; ok {
		cfg.BaseURL = v
	}
	if v, ok := settings["bufferSizePerStream"]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.BufferSizePerStream = n
		}
	}
	if v, ok := settings["cacheEnabled"]; ok {
		cfg.CacheEnabled = v == "true"
	}
	if v, ok := settings["cacheDuration"]; ok {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.CacheDuration = d
		}
	}
	if v, ok := settings["importRefreshInterval"]; ok {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.ImportRefreshInterval = d
		}
	}
	if v, ok := settings["workerThreads"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.WorkerThreads = n
		}
	}
	if v, ok := settings["debug"]; ok {
		cfg.Debug = v == "true"
	}
	if v, ok := settings["logLevel"]; ok {
		cfg.LogLevel = v
	}
	if v, ok := settings["obfuscateUrls"]; ok {
		cfg.ObfuscateUrls = v == "true"
	}
	if v, ok := settings["sortField"]; ok {
		cfg.SortField = v
	}
	if v, ok := settings["sortDirection"]; ok {
		cfg.SortDirection = v
	}
	if v, ok := settings["streamTimeout"]; ok {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.StreamTimeout = d
		}
	}
	if v, ok := settings["maxConnectionsToApp"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxConnectionsToApp = n
		}
	}
	if v, ok := settings["watcherEnabled"]; ok {
		cfg.WatcherEnabled = v == "true"
	}
	if v, ok := settings["ffmpegMode"]; ok {
		cfg.FFmpegMode = v == "true"
	}
	if v, ok := settings["ffmpegPreInput"]; ok && v != "" {
		cfg.FFmpegPreInput = strings.Fields(v)
	}
	if v, ok := settings["ffmpegPreOutput"]; ok && v != "" {
		cfg.FFmpegPreOutput = strings.Fields(v)
	}
	if v, ok := settings["responseHeaderTimeout"]; ok {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.ResponseHeaderTimeout = d
		}
	}
	if v, ok := settings["slowClientBufferChunks"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.SlowClientBufferChunks = n
		}
	}

	if v, ok := settings["tmdbEnabled"]; ok {
		cfg.TMDBEnabled = v == "true"
	}
	if v, ok := settings["tmdbApiKey"]; ok {
		cfg.TMDBAPIKey = v
	}

	cfg.Sources, err = loadSourcesFromDB()
	if err != nil {
		return nil, err
	}
	cfg.EPGs, err = loadEPGsFromDB()
	if err != nil {
		return nil, err
	}
	cfg.XCOutputAccounts, err = loadXCAccountsFromDB()
	if err != nil {
		return nil, err
	}
	cfg.SDAccounts, err = loadSDAccountsFromDB()
	if err != nil {
		return nil, err
	}
	cfg.FilterProfiles, err = loadFilterProfilesFromDB()
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

// loadSourcesFromDB converts kp_sources rows into SourceConfig values.
func loadSourcesFromDB() ([]SourceConfig, error) {
	rows, err := db.GetAllSources()
	if err != nil {
		return nil, err
	}
	sources := make([]SourceConfig, 0, len(rows))
	for _, r := range rows {
		maxStreamTo, err := time.ParseDuration(r.MaxStreamTo)
		if err != nil {
			maxStreamTo = 30 * time.Second
		}
		retryDelay, err := time.ParseDuration(r.RetryDelay)
		if err != nil {
			retryDelay = 5 * time.Second
		}
		sources = append(sources, SourceConfig{
			Name:                   r.Name,
			URL:                    r.URI,
			Order:                  r.SortOrder,
			MaxConnections:         r.MaxCnx,
			MaxStreamTimeout:       maxStreamTo,
			RetryDelay:             retryDelay,
			MaxRetries:             r.MaxRetries,
			MaxFailuresBeforeBlock: r.MaxFailures,
			MinDataSize:            int64(r.MinDataSize),
			UserAgent:              r.UserAgent,
			ReqOrigin:              r.ReqOrigin,
			ReqReferrer:            r.ReqReferer,
			Username:               r.Username,
			Password:               r.Password,
			LiveIncludeRegex:       r.LiveIncRegex,
			LiveExcludeRegex:       r.LiveExcRegex,
			SeriesIncludeRegex:     r.SeriesIncRegex,
			SeriesExcludeRegex:     r.SeriesExcRegex,
			VODIncludeRegex:        r.VODIncRegex,
			VODExcludeRegex:        r.VODExcRegex,
			LiveCategoryRegex:      r.LiveCatRegex,
			VODCategoryRegex:       r.VODCatRegex,
			SeriesCategoryRegex:    r.SeriesCatRegex,
			GroupFilterMode:        r.GroupFilterMode,
			GroupFilterList:        decodeStringList(r.GroupFilterList),
			GroupFilterRegex:       r.GroupFilterRegex,
			ImportTypes:            decodeStringList(r.ImportTypes),
			GroupTypeOverrides:     decodeStringMap(r.GroupTypeOverrides),
			FilterProfile:          r.FilterProfile,
			FilterRules:            DecodeRules(r.FilterRules),
			FilterDefault:          r.FilterDefault,
			QualityDedupe:          r.QualityDedupe == "1",
			QualityTiers:           decodeStringList(r.QualityTiers),
		})
	}
	return sources, nil
}

// loadFilterProfilesFromDB converts kp_filter_profiles rows into the shared
// rule sets sources reference by name.
func loadFilterProfilesFromDB() ([]FilterProfile, error) {
	rows, err := db.GetAllFilterProfiles()
	if err != nil {
		return nil, err
	}
	profiles := make([]FilterProfile, 0, len(rows))
	for _, r := range rows {
		profiles = append(profiles, FilterProfile{
			ID:      r.ID,
			Name:    r.Name,
			Default: r.DefaultAction,
			Rules:   DecodeRules(r.Rules),
		})
	}
	return profiles, nil
}

// loadEPGsFromDB converts kp_epgs rows into EPGConfig values.
func loadEPGsFromDB() ([]EPGConfig, error) {
	rows, err := db.GetAllEPGs()
	if err != nil {
		return nil, err
	}
	epgs := make([]EPGConfig, 0, len(rows))
	for _, r := range rows {
		epgs = append(epgs, EPGConfig{
			Name:  r.Name,
			URL:   r.URL,
			Order: r.SortOrder,
		})
	}
	return epgs, nil
}

// loadXCAccountsFromDB converts kp_xc_accounts rows into XCOutputAccount values.
func loadXCAccountsFromDB() ([]XCOutputAccount, error) {
	rows, err := db.GetAllXCAccounts()
	if err != nil {
		return nil, err
	}
	accounts := make([]XCOutputAccount, 0, len(rows))
	for _, r := range rows {
		accounts = append(accounts, XCOutputAccount{
			Name:           r.Name,
			Username:       r.Username,
			Password:       r.Password,
			MaxConnections: r.MaxCnx,
			EnableLive:     r.EnableLive,
			EnableSeries:   r.EnableSeries,
			EnableVOD:      r.EnableVOD,
		})
	}
	return accounts, nil
}

// loadSDAccountsFromDB converts kp_sd_accounts rows (with lineups) into SDAccount values.
func loadSDAccountsFromDB() ([]SDAccount, error) {
	rows, err := db.GetAllSDAccountsWithLineups()
	if err != nil {
		return nil, err
	}
	accounts := make([]SDAccount, 0, len(rows))
	for _, r := range rows {
		accounts = append(accounts, SDAccount{
			Name:            r.Name,
			Username:        r.Username,
			Password:        r.Password,
			Enabled:         r.Enabled,
			DaysToFetch:     r.DaysToFetch,
			SelectedLineups: r.Lineups,
		})
	}
	return accounts, nil
}

// PersistConfig writes every field of cfg into kp_settings and syncs sources.
func PersistConfig(cfg *Config) error {
	settings := map[string]string{
		"baseURL":                cfg.BaseURL,
		"bufferSizePerStream":    strconv.FormatInt(cfg.BufferSizePerStream, 10),
		"cacheEnabled":           strconv.FormatBool(cfg.CacheEnabled),
		"cacheDuration":          cfg.CacheDuration.String(),
		"importRefreshInterval":  cfg.ImportRefreshInterval.String(),
		"workerThreads":          strconv.Itoa(cfg.WorkerThreads),
		"debug":                  strconv.FormatBool(cfg.Debug),
		"logLevel":               cfg.LogLevel,
		"obfuscateUrls":          strconv.FormatBool(cfg.ObfuscateUrls),
		"sortField":              cfg.SortField,
		"sortDirection":          cfg.SortDirection,
		"streamTimeout":          cfg.StreamTimeout.String(),
		"maxConnectionsToApp":    strconv.Itoa(cfg.MaxConnectionsToApp),
		"watcherEnabled":         strconv.FormatBool(cfg.WatcherEnabled),
		"ffmpegMode":             strconv.FormatBool(cfg.FFmpegMode),
		"ffmpegPreInput":         strings.Join(cfg.FFmpegPreInput, " "),
		"ffmpegPreOutput":        strings.Join(cfg.FFmpegPreOutput, " "),
		"responseHeaderTimeout":  cfg.ResponseHeaderTimeout.String(),
		"slowClientBufferChunks": strconv.Itoa(cfg.SlowClientBufferChunks),
		"tmdbEnabled":            strconv.FormatBool(cfg.TMDBEnabled),
		"tmdbApiKey":             cfg.TMDBAPIKey,
	}

	for k, v := range settings {
		if err := db.SetSetting(k, v); err != nil {
			return err
		}
	}

	return syncSourcesToDB(cfg.Sources)
}

// syncSourcesToDB replaces all kp_sources rows to match cfg.
func syncSourcesToDB(sources []SourceConfig) error {
	if _, err := db.Get().Exec(`DELETE FROM kp_sources`); err != nil {
		return err
	}
	for i := range sources {
		s := &sources[i]
		if _, err := db.InsertSource(db.Source{
			Name:               s.Name,
			URI:                s.URL,
			Username:           s.Username,
			Password:           s.Password,
			SortOrder:          s.Order,
			MaxCnx:             s.MaxConnections,
			MaxStreamTo:        s.MaxStreamTimeout.String(),
			RetryDelay:         s.RetryDelay.String(),
			MaxRetries:         s.MaxRetries,
			MaxFailures:        s.MaxFailuresBeforeBlock,
			MinDataSize:        int(s.MinDataSize),
			UserAgent:          s.UserAgent,
			ReqOrigin:          s.ReqOrigin,
			ReqReferer:         s.ReqReferrer,
			LiveIncRegex:       s.LiveIncludeRegex,
			LiveExcRegex:       s.LiveExcludeRegex,
			SeriesIncRegex:     s.SeriesIncludeRegex,
			SeriesExcRegex:     s.SeriesExcludeRegex,
			VODIncRegex:        s.VODIncludeRegex,
			VODExcRegex:        s.VODExcludeRegex,
			LiveCatRegex:       s.LiveCategoryRegex,
			VODCatRegex:        s.VODCategoryRegex,
			SeriesCatRegex:     s.SeriesCategoryRegex,
			GroupFilterMode:    s.GroupFilterMode,
			GroupFilterList:    encodeStringList(s.GroupFilterList),
			GroupFilterRegex:   s.GroupFilterRegex,
			ImportTypes:        encodeStringList(s.ImportTypes),
			GroupTypeOverrides: encodeStringMap(s.GroupTypeOverrides),
			FilterProfile:      s.FilterProfile,
			FilterRules:        EncodeRules(s.FilterRules),
			FilterDefault:      s.FilterDefault,
			QualityDedupe:      encodeBool(s.QualityDedupe),
			QualityTiers:       encodeStringList(s.QualityTiers),
		}); err != nil {
			return err
		}
	}

	keep := make([]string, 0, len(sources))
	for i := range sources {
		keep = append(keep, sources[i].URL)
	}
	if err := db.PruneSourceData(keep); err != nil {
		return err
	}
	return db.PruneSeriesInfo(keep)
}

// syncEPGsToDB replaces all kp_epgs rows to match cfg.
func syncEPGsToDB(epgs []EPGConfig) error {
	if _, err := db.Get().Exec(`DELETE FROM kp_epgs`); err != nil {
		return err
	}
	for _, e := range epgs {
		if _, err := db.InsertEPG(db.EPG{
			Name:      e.Name,
			URL:       e.URL,
			SortOrder: e.Order,
		}); err != nil {
			return err
		}
	}
	return nil
}

// syncXCAccountsToDB replaces all kp_xc_accounts rows to match cfg.
func syncXCAccountsToDB(accounts []XCOutputAccount) error {
	if _, err := db.Get().Exec(`DELETE FROM kp_xc_accounts`); err != nil {
		return err
	}
	for i := range accounts {
		a := &accounts[i]
		if _, err := db.InsertXCAccount(db.XCAccount{
			Name:         a.Name,
			Username:     a.Username,
			Password:     a.Password,
			MaxCnx:       a.MaxConnections,
			EnableLive:   a.EnableLive,
			EnableSeries: a.EnableSeries,
			EnableVOD:    a.EnableVOD,
		}); err != nil {
			return err
		}
	}
	return nil
}

// syncSDAccountsToDB replaces all kp_sd_accounts rows (and their lineups) to match cfg.
func syncSDAccountsToDB(accounts []SDAccount) error {
	if _, err := db.Get().Exec(`DELETE FROM kp_sd_accounts`); err != nil {
		return err
	}
	for _, a := range accounts {
		if _, err := db.InsertSDAccount(db.SDAccount{
			Name:        a.Name,
			Username:    a.Username,
			Password:    a.Password,
			Enabled:     a.Enabled,
			DaysToFetch: a.DaysToFetch,
			Lineups:     a.SelectedLineups,
		}); err != nil {
			return err
		}
	}
	return nil
}

// getDefaultConfig returns a baseline configuration with sensible defaults.
func getDefaultConfig() *Config {
	return &Config{
		BaseURL:               "http://localhost:8080",
		BufferSizePerStream:   1,
		CacheEnabled:          true,
		CacheDuration:         30 * time.Minute,
		ImportRefreshInterval: 12 * time.Hour,
		WorkerThreads:         8,
		Debug:                 true,
		LogLevel:              "INFO",
		ObfuscateUrls:         false,
		SortField:             "tvg-name",
		SortDirection:         "asc",
		StreamTimeout:         10 * time.Second,
		MaxConnectionsToApp:   100,
		Sources:               []SourceConfig{},
		EPGs:                  []EPGConfig{},
		WatcherEnabled:        true,
		FFmpegPreInput:        []string{},
		FFmpegPreOutput:       []string{},
		ResponseHeaderTimeout: 10 * time.Second,
	}
}

// validateAndSetDefaults ensures all config values are valid.
func validateAndSetDefaults(config *Config) {
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:8080"
	}
	if config.BufferSizePerStream <= 0 {
		config.BufferSizePerStream = 1
	}
	if config.CacheDuration <= 0 {
		config.CacheDuration = 30 * time.Minute
	}
	if config.ImportRefreshInterval <= 0 {
		config.ImportRefreshInterval = 12 * time.Hour
	}
	if config.ResponseHeaderTimeout <= 0 {
		config.ResponseHeaderTimeout = 10 * time.Second
	}
	if config.WorkerThreads <= 0 {
		config.WorkerThreads = 8
	}
	if config.SortField == "" {
		config.SortField = "tvg-name"
	}
	if config.SortDirection == "" {
		config.SortDirection = "asc"
	}
	if config.StreamTimeout <= 0 {
		config.StreamTimeout = 10 * time.Second
	}
	if config.MaxConnectionsToApp <= 0 {
		config.MaxConnectionsToApp = 100
	}
	if config.LogLevel == "" {
		config.LogLevel = "INFO"
	}
	if config.FFmpegPreInput == nil {
		config.FFmpegPreInput = []string{}
	}
	if config.FFmpegPreOutput == nil {
		config.FFmpegPreOutput = []string{}
	}
	if config.SlowClientBufferChunks <= 0 {
		config.SlowClientBufferChunks = 128
	}
	for i := range config.XCOutputAccounts {
		if config.XCOutputAccounts[i].MaxConnections <= 0 {
			config.XCOutputAccounts[i].MaxConnections = 10
		}
	}
	for i := range config.Sources {
		src := &config.Sources[i]
		if src.Name == "" {
			src.Name = strconv.Itoa(i + 1)
		}
		if src.Order <= 0 {
			src.Order = i + 1
		}
		if src.MaxConnections <= 0 {
			src.MaxConnections = 5
		}
		if src.MaxStreamTimeout <= 0 {
			src.MaxStreamTimeout = 30 * time.Second
		}
		if src.RetryDelay <= 0 {
			src.RetryDelay = 5 * time.Second
		}
		if src.MaxRetries <= 0 {
			src.MaxRetries = 3
		}
		if src.MaxFailuresBeforeBlock <= 0 {
			src.MaxFailuresBeforeBlock = 5
		}
		if src.MinDataSize <= 0 {
			src.MinDataSize = 1
		}
		if src.UserAgent == "" {
			src.UserAgent = "VLC/3.0.18 LibVLC/3.0.18"
		}
	}
}

// GetSourceByURL returns a pointer to the SourceConfig matching the given URL.
func (c *Config) GetSourceByURL(url string) *SourceConfig {
	for i := range c.Sources {
		if c.Sources[i].URL == url {
			return &c.Sources[i]
		}
	}
	return nil
}
