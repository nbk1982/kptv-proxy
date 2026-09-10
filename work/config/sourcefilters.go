// work/config/sourcefilters.go
package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/grafana/regexp"
)

// Group filter modes. Off imports every group; include keeps only the listed or
// matching groups; exclude drops them and keeps the rest.
const (
	GroupFilterOff     = ""
	GroupFilterInclude = "include"
	GroupFilterExclude = "exclude"
)

// contentTypeNames are the values ImportTypes and GroupTypeOverrides accept.
// They mirror types.ContentType, which cannot be imported here because types
// depends on this package.
var contentTypeNames = []string{"live", "vod", "series"}

// isContentTypeName reports whether v names a content type.
func isContentTypeName(v string) bool {
	for _, name := range contentTypeNames {
		if v == name {
			return true
		}
	}
	return false
}

// GroupKey normalizes a group label for comparison: lowercased, trimmed and
// with internal whitespace collapsed, so "♦️  GLOBO" and "♦️ globo" name the
// same group. Providers are not consistent about spacing from one playlist
// refresh to the next, and a selection made in the UI must survive that.
func GroupKey(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}

// sourceAlias is the wire shape of a source in the admin API and the legacy
// config file: durations travel as strings such as "30s".
type sourceAlias struct {
	Name                   string            `json:"name"`
	URL                    string            `json:"url"`
	Order                  int               `json:"order"`
	MaxConnections         int               `json:"maxConnections"`
	MaxStreamTimeout       string            `json:"maxStreamTimeout"`
	RetryDelay             string            `json:"retryDelay"`
	MaxRetries             int               `json:"maxRetries"`
	MaxFailuresBeforeBlock int               `json:"maxFailuresBeforeBlock"`
	MinDataSize            int64             `json:"minDataSize"`
	UserAgent              string            `json:"userAgent"`
	ReqOrigin              string            `json:"reqOrigin"`
	ReqReferrer            string            `json:"reqReferrer"`
	Username               string            `json:"username"`
	Password               string            `json:"password"`
	LiveIncludeRegex       string            `json:"liveIncludeRegex"`
	LiveExcludeRegex       string            `json:"liveExcludeRegex"`
	SeriesIncludeRegex     string            `json:"seriesIncludeRegex"`
	SeriesExcludeRegex     string            `json:"seriesExcludeRegex"`
	VODIncludeRegex        string            `json:"vodIncludeRegex"`
	VODExcludeRegex        string            `json:"vodExcludeRegex"`
	LiveCategoryRegex      string            `json:"liveCategoryRegex"`
	VODCategoryRegex       string            `json:"vodCategoryRegex"`
	SeriesCategoryRegex    string            `json:"seriesCategoryRegex"`
	GroupFilterMode        string            `json:"groupFilterMode"`
	GroupFilterList        []string          `json:"groupFilterList"`
	GroupFilterRegex       string            `json:"groupFilterRegex"`
	ImportTypes            []string          `json:"importTypes"`
	GroupTypeOverrides     map[string]string `json:"groupTypeOverrides"`
}

// fill copies the alias into dst, parsing the duration strings. It writes
// through a pointer because SourceConfig carries an atomic counter that must
// not be copied by value.
func (s sourceAlias) fill(dst *SourceConfig) error {
	*dst = SourceConfig{
		Name:                   s.Name,
		URL:                    s.URL,
		Order:                  s.Order,
		MaxConnections:         s.MaxConnections,
		MaxRetries:             s.MaxRetries,
		MaxFailuresBeforeBlock: s.MaxFailuresBeforeBlock,
		MinDataSize:            s.MinDataSize,
		UserAgent:              s.UserAgent,
		ReqOrigin:              s.ReqOrigin,
		ReqReferrer:            s.ReqReferrer,
		Username:               s.Username,
		Password:               s.Password,
		LiveIncludeRegex:       s.LiveIncludeRegex,
		LiveExcludeRegex:       s.LiveExcludeRegex,
		SeriesIncludeRegex:     s.SeriesIncludeRegex,
		SeriesExcludeRegex:     s.SeriesExcludeRegex,
		VODIncludeRegex:        s.VODIncludeRegex,
		VODExcludeRegex:        s.VODExcludeRegex,
		LiveCategoryRegex:      s.LiveCategoryRegex,
		VODCategoryRegex:       s.VODCategoryRegex,
		SeriesCategoryRegex:    s.SeriesCategoryRegex,
		GroupFilterMode:        s.GroupFilterMode,
		GroupFilterList:        s.GroupFilterList,
		GroupFilterRegex:       s.GroupFilterRegex,
		ImportTypes:            s.ImportTypes,
		GroupTypeOverrides:     s.GroupTypeOverrides,
	}

	var err error
	if s.MaxStreamTimeout != "" {
		if dst.MaxStreamTimeout, err = time.ParseDuration(s.MaxStreamTimeout); err != nil {
			return fmt.Errorf("invalid maxStreamTimeout for source %s: %w", s.Name, err)
		}
	}
	if s.RetryDelay != "" {
		if dst.RetryDelay, err = time.ParseDuration(s.RetryDelay); err != nil {
			return fmt.Errorf("invalid retryDelay for source %s: %w", s.Name, err)
		}
	}
	return nil
}

// ParseSourceJSON decodes one source exactly as the admin API sends it inside
// a config document, so the preview endpoint accepts the same payload the
// save does.
func ParseSourceJSON(data []byte, dst *SourceConfig) error {
	var alias sourceAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	return alias.fill(dst)
}

// HasContentFilters reports whether any import-time filter is configured, so
// the engine can skip the pattern work for a source that keeps everything.
func (s *SourceConfig) HasContentFilters() bool {
	return s.GroupFilterMode != GroupFilterOff || s.GroupFilterRegex != "" ||
		len(s.ImportTypes) > 0 || len(s.GroupTypeOverrides) > 0 ||
		s.LiveCategoryRegex != "" || s.VODCategoryRegex != "" || s.SeriesCategoryRegex != "" ||
		s.LiveIncludeRegex != "" || s.LiveExcludeRegex != "" ||
		s.SeriesIncludeRegex != "" || s.SeriesExcludeRegex != "" ||
		s.VODIncludeRegex != "" || s.VODExcludeRegex != ""
}

// ImportsType reports whether streams of the named content type are imported.
// An empty ImportTypes list means every type, which is what every source
// configured before the gate existed expects.
func (s *SourceConfig) ImportsType(contentType string) bool {
	if len(s.ImportTypes) == 0 {
		return true
	}
	for _, t := range s.ImportTypes {
		if t == contentType {
			return true
		}
	}
	return false
}

// NormalizeFilters tidies operator input in place and reports the first value
// the import could not honour. It runs on every save and before every preview,
// so a bad pattern is rejected with a message naming the field rather than
// silently disabled at import time, which is what compilePattern in the filter
// engine would otherwise do.
func (s *SourceConfig) NormalizeFilters() error {
	switch s.GroupFilterMode {
	case GroupFilterOff, GroupFilterInclude, GroupFilterExclude:
	default:
		return fmt.Errorf("source %q: groupFilterMode %q is not one of \"\", %q, %q", s.Name, s.GroupFilterMode, GroupFilterInclude, GroupFilterExclude)
	}

	s.GroupFilterList = dedupeGroups(s.GroupFilterList)
	s.GroupFilterRegex = strings.TrimSpace(s.GroupFilterRegex)
	if s.GroupFilterMode == GroupFilterInclude && len(s.GroupFilterList) == 0 && s.GroupFilterRegex == "" {
		return fmt.Errorf("source %q: \"only selected groups\" keeps nothing; select at least one group or add a group pattern", s.Name)
	}

	types := make([]string, 0, len(s.ImportTypes))
	seen := make(map[string]bool, len(contentTypeNames))
	for _, t := range s.ImportTypes {
		t = strings.ToLower(strings.TrimSpace(t))
		if !isContentTypeName(t) {
			return fmt.Errorf("source %q: importTypes value %q is not one of live, vod, series", s.Name, t)
		}
		if !seen[t] {
			seen[t] = true
			types = append(types, t)
		}
	}
	// every type selected is the same as no gate, and is stored that way so
	// the config reads the same as one written before the gate existed
	if len(types) == 0 || len(types) == len(contentTypeNames) {
		types = nil
	}
	s.ImportTypes = types

	var overrides map[string]string
	for group, t := range s.GroupTypeOverrides {
		group = strings.TrimSpace(group)
		t = strings.ToLower(strings.TrimSpace(t))
		if group == "" || t == "" {
			continue
		}
		if !isContentTypeName(t) {
			return fmt.Errorf("source %q: groupTypeOverrides[%q] value %q is not one of live, vod, series", s.Name, group, t)
		}
		if overrides == nil {
			overrides = make(map[string]string)
		}
		overrides[group] = t
	}
	s.GroupTypeOverrides = overrides

	for _, p := range []struct{ field, pattern string }{
		{"groupFilterRegex", s.GroupFilterRegex},
		{"liveCategoryRegex", s.LiveCategoryRegex},
		{"vodCategoryRegex", s.VODCategoryRegex},
		{"seriesCategoryRegex", s.SeriesCategoryRegex},
		{"liveIncludeRegex", s.LiveIncludeRegex},
		{"liveExcludeRegex", s.LiveExcludeRegex},
		{"seriesIncludeRegex", s.SeriesIncludeRegex},
		{"seriesExcludeRegex", s.SeriesExcludeRegex},
		{"vodIncludeRegex", s.VODIncludeRegex},
		{"vodExcludeRegex", s.VODExcludeRegex},
	} {
		if p.pattern == "" {
			continue
		}
		if _, err := regexp.Compile(p.pattern); err != nil {
			return fmt.Errorf("source %q: %s %q is not a valid pattern: %v", s.Name, p.field, p.pattern, err)
		}
	}
	return nil
}

// dedupeGroups trims the labels, drops empty ones and collapses entries that
// GroupKey considers equal, keeping the first spelling the operator used.
func dedupeGroups(groups []string) []string {
	if len(groups) == 0 {
		return nil
	}
	out := make([]string, 0, len(groups))
	seen := make(map[string]bool, len(groups))
	for _, g := range groups {
		g = strings.TrimSpace(g)
		key := GroupKey(g)
		if g == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, g)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// encodeStringList serializes a list for a TEXT column; empty stays "" so the
// column default and "nothing configured" are the same value.
func encodeStringList(v []string) string {
	if len(v) == 0 {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// decodeStringList reverses encodeStringList; malformed text reads as empty
// rather than taking the source offline.
func decodeStringList(raw string) []string {
	if raw == "" {
		return nil
	}
	var v []string
	if err := json.Unmarshal([]byte(raw), &v); err != nil || len(v) == 0 {
		return nil
	}
	return v
}

// encodeStringMap serializes a map for a TEXT column, "" when empty.
func encodeStringMap(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// decodeStringMap reverses encodeStringMap; malformed text reads as empty.
func decodeStringMap(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil || len(m) == 0 {
		return nil
	}
	return m
}
