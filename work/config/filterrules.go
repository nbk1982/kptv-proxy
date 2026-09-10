// work/config/filterrules.go
package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grafana/regexp"
)

// Fields a rule can be tested against. "group" is the provider's group label
// (group-title, or tvg-group when that is empty), "name" the stream's imported
// name, "url" its address and "any" all three.
const (
	FilterFieldGroup = "group"
	FilterFieldName  = "name"
	FilterFieldURL   = "url"
	FilterFieldAny   = "any"
)

// What a matching rule decides.
const (
	FilterActionInclude = "include"
	FilterActionExclude = "exclude"
)

// What happens to a stream no rule matched. Keep is the default, so adding an
// exclude rule to a source that had none drops only what it names.
const (
	FilterDefaultKeep = "keep"
	FilterDefaultDrop = "drop"
)

// Bounds on operator input, so one source cannot make an import crawl or a
// config document unreasonable to store.
const (
	MaxFilterRules      = 200
	MaxFilterPatternLen = 500
)

// FilterRule is one ordered decision: if Pattern matches the chosen field,
// Action settles whether the stream is imported and no later rule is consulted.
type FilterRule struct {
	Field   string `json:"field"`          // FilterFieldGroup, FilterFieldName, FilterFieldURL or FilterFieldAny
	Action  string `json:"action"`         // FilterActionInclude or FilterActionExclude
	Pattern string `json:"pattern"`        // regular expression, matched case-insensitively
	Note    string `json:"note,omitempty"` // free-text reminder shown beside the rule
}

// FilterProfile is a rule set that lives on its own so several sources can
// share it. A source names one in FilterProfile and may add its own rules
// after it.
type FilterProfile struct {
	ID      int64        `json:"id"`
	Name    string       `json:"name"`
	Default string       `json:"default,omitempty"` // FilterDefaultKeep or FilterDefaultDrop
	Rules   []FilterRule `json:"rules"`
}

// filterFieldNames and filterActionNames back the validation messages.
var (
	filterFieldNames  = []string{FilterFieldGroup, FilterFieldName, FilterFieldURL, FilterFieldAny}
	filterActionNames = []string{FilterActionInclude, FilterActionExclude}
)

// contains reports whether v is one of the allowed values.
func contains(allowed []string, v string) bool {
	for _, a := range allowed {
		if a == v {
			return true
		}
	}
	return false
}

// CompileFilterPattern compiles a rule pattern. Patterns are matched
// case-insensitively unless the operator opens with their own flag group, so
// a label copied out of a playlist works whatever case it carries.
func CompileFilterPattern(pattern string) (*regexp.Regexp, error) {
	if strings.HasPrefix(pattern, "(?") {
		return regexp.Compile(pattern)
	}
	return regexp.Compile("(?i)" + pattern)
}

// NormalizeRules trims and validates an ordered rule list in place, describing
// the offending rule by its position when it cannot be honoured.
func NormalizeRules(rules []FilterRule, owner string) ([]FilterRule, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	if len(rules) > MaxFilterRules {
		return nil, fmt.Errorf("%s: %d rules is more than the %d allowed", owner, len(rules), MaxFilterRules)
	}

	out := make([]FilterRule, 0, len(rules))
	for i, rule := range rules {
		rule.Field = strings.ToLower(strings.TrimSpace(rule.Field))
		rule.Action = strings.ToLower(strings.TrimSpace(rule.Action))
		rule.Note = strings.TrimSpace(rule.Note)
		// the pattern is kept exactly as typed: whitespace is significant in a
		// regular expression, and providers do mark whole families of groups
		// with a leading or trailing run of spaces

		if rule.Field == "" {
			rule.Field = FilterFieldGroup
		}
		if rule.Action == "" {
			rule.Action = FilterActionInclude
		}
		// an empty pattern would match everything and shadow every later
		// rule, which is never what an operator half-way through typing meant
		if rule.Pattern == "" {
			continue
		}
		if len(rule.Pattern) > MaxFilterPatternLen {
			return nil, fmt.Errorf("%s: rule %d pattern is longer than %d characters", owner, i+1, MaxFilterPatternLen)
		}
		if !contains(filterFieldNames, rule.Field) {
			return nil, fmt.Errorf("%s: rule %d field %q is not one of %s", owner, i+1, rule.Field, strings.Join(filterFieldNames, ", "))
		}
		if !contains(filterActionNames, rule.Action) {
			return nil, fmt.Errorf("%s: rule %d action %q is not one of %s", owner, i+1, rule.Action, strings.Join(filterActionNames, ", "))
		}
		if _, err := CompileFilterPattern(rule.Pattern); err != nil {
			return nil, fmt.Errorf("%s: rule %d pattern %q is not valid: %v", owner, i+1, rule.Pattern, err)
		}
		out = append(out, rule)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// normalizeDefault validates the fallback verdict, treating the empty value as
// keep so a rule list that only excludes behaves the way it reads.
func normalizeDefault(value, owner string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", FilterDefaultKeep:
		return "", nil
	case FilterDefaultDrop:
		return FilterDefaultDrop, nil
	}
	return "", fmt.Errorf("%s: default %q is not %s or %s", owner, value, FilterDefaultKeep, FilterDefaultDrop)
}

// Normalize tidies and validates a profile in place.
func (p *FilterProfile) Normalize() error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return fmt.Errorf("a filter profile needs a name")
	}
	owner := fmt.Sprintf("profile %q", p.Name)

	var err error
	if p.Default, err = normalizeDefault(p.Default, owner); err != nil {
		return err
	}
	if p.Rules, err = NormalizeRules(p.Rules, owner); err != nil {
		return err
	}
	return nil
}

// EncodeRules serializes a rule list for a TEXT column, "" when empty.
func EncodeRules(rules []FilterRule) string {
	if len(rules) == 0 {
		return ""
	}
	b, err := json.Marshal(rules)
	if err != nil {
		return ""
	}
	return string(b)
}

// DecodeRules reverses EncodeRules; malformed text reads as no rules rather
// than taking a source offline.
func DecodeRules(raw string) []FilterRule {
	if raw == "" {
		return nil
	}
	var rules []FilterRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil || len(rules) == 0 {
		return nil
	}
	return rules
}

// EffectiveRules returns the rules a source is filtered by: those of the
// profile it names, then its own. RuleOrigin describes where each came from so
// a preview can attribute matches to the profile or to the source.
func (c *Config) EffectiveRules(source *SourceConfig) ([]FilterRule, []string) {
	var rules []FilterRule
	var origins []string

	if source.FilterProfile != "" {
		if profile := c.FilterProfileByName(source.FilterProfile); profile != nil {
			for _, rule := range profile.Rules {
				rules = append(rules, rule)
				origins = append(origins, "profile:"+profile.Name)
			}
		}
	}
	for _, rule := range source.FilterRules {
		rules = append(rules, rule)
		origins = append(origins, "source")
	}
	return rules, origins
}

// EffectiveDefault returns the verdict for a stream no rule matched: the
// source's own choice when it made one, otherwise the profile's, otherwise
// keep.
func (c *Config) EffectiveDefault(source *SourceConfig) string {
	if source.FilterDefault != "" {
		return source.FilterDefault
	}
	if source.FilterProfile != "" {
		if profile := c.FilterProfileByName(source.FilterProfile); profile != nil && profile.Default != "" {
			return profile.Default
		}
	}
	return FilterDefaultKeep
}

// FilterProfileByName finds a profile by name, or nil.
func (c *Config) FilterProfileByName(name string) *FilterProfile {
	for i := range c.FilterProfiles {
		if c.FilterProfiles[i].Name == name {
			return &c.FilterProfiles[i]
		}
	}
	return nil
}
