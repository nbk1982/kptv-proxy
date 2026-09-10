// work/filter/rules.go
package filter

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/types"

	"github.com/grafana/regexp"
)

// compiledRule is one rule of the ordered list, ready to test.
type compiledRule struct {
	field   string
	include bool
	pattern *regexp.Regexp
	origin  string // "source", or "profile:<name>", for attributing matches in a preview
	rule    config.FilterRule
}

// compileRules compiles an ordered rule list. A rule whose pattern does not
// compile is dropped with a log line rather than taking the source offline;
// the admin API rejects such a pattern before it can be stored, so this only
// covers a value written before that validation existed.
func compileRules(rules []config.FilterRule, origins []string) []compiledRule {
	if len(rules) == 0 {
		return nil
	}
	compiled := make([]compiledRule, 0, len(rules))
	for i, rule := range rules {
		pattern, err := config.CompileFilterPattern(rule.Pattern)
		if err != nil {
			logger.Error("{filter - compileRules} rule %d pattern '%s' does not compile: %v", i+1, rule.Pattern, err)
			continue
		}
		origin := "source"
		if i < len(origins) {
			origin = origins[i]
		}
		compiled = append(compiled, compiledRule{
			field:   rule.Field,
			include: rule.Action == config.FilterActionInclude,
			pattern: pattern,
			origin:  origin,
			rule:    rule,
		})
	}
	return compiled
}

// ruleSubjects returns the strings a rule field is tested against. Values are
// the provider's own, untouched: rule patterns are compiled case-insensitively,
// so a label can be pasted straight out of a playlist.
func ruleSubjects(stream *types.Stream, group, field string) []string {
	switch field {
	case config.FilterFieldGroup:
		return []string{group}
	case config.FilterFieldName:
		return []string{stream.Name}
	case config.FilterFieldURL:
		return []string{stream.URL}
	default:
		return []string{group, stream.Name, stream.URL}
	}
}

// matchesRule reports whether a rule's pattern hits any of its subjects.
func matchesRule(rule *compiledRule, stream *types.Stream, group string) bool {
	for _, subject := range ruleSubjects(stream, group, rule.field) {
		if subject != "" && rule.pattern.MatchString(subject) {
			return true
		}
	}
	return false
}

// ruleVerdict walks the rules in order and returns the first decision, the
// index that made it, and whether any rule matched at all. With collectAll the
// walk continues past the decision so a preview can report how many streams
// each later rule would have matched, which is how an operator sees that a
// rule is shadowed rather than simply wrong.
func ruleVerdict(rules []compiledRule, stream *types.Stream, group string, collectAll bool, matched []int) (keep bool, decidedBy int, decided bool) {
	decidedBy = -1
	for i := range rules {
		if !matchesRule(&rules[i], stream, group) {
			continue
		}
		if matched != nil {
			matched[i]++
		}
		if !decided {
			keep, decidedBy, decided = rules[i].include, i, true
			if !collectAll {
				return keep, decidedBy, decided
			}
		}
	}
	return keep, decidedBy, decided
}
