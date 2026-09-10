// work/filter/report.go
package filter

import (
	"sort"

	"kptv-proxy/work/config"
	"kptv-proxy/work/types"
)

const (
	maxGroupSamples = 3  // stream names kept per group so an operator can recognise it
	maxKeptSamples  = 40 // surviving stream names shown in a preview
)

// TypeStat counts the streams of one content type before and after filtering.
type TypeStat struct {
	Total int `json:"total"`
	Kept  int `json:"kept"`
}

// GroupStat describes one group of a source's catalog as the filter saw it.
type GroupStat struct {
	Name        string            `json:"name"`    // the provider's label; "" for streams without a group
	ContentType types.ContentType `json:"type"`    // the type most of the group's streams resolved to
	Total       int               `json:"total"`   // streams in the group before filtering
	Kept        int               `json:"kept"`    // streams that survived every stage
	Samples     []string          `json:"samples"` // a few stream names from the group
	typeCounts  map[types.ContentType]int
}

// RuleStat describes how one rule of the ordered list performed.
type RuleStat struct {
	Index   int    `json:"index"`   // position in the effective list, from 1
	Origin  string `json:"origin"`  // "source", or "profile:<name>"
	Field   string `json:"field"`   // group, name, url or any
	Action  string `json:"action"`  // include or exclude
	Pattern string `json:"pattern"` // the rule as written
	Note    string `json:"note"`    // the operator's reminder, if any
	Matched int    `json:"matched"` // streams the pattern hit, whichever rule decided them
	Decided int    `json:"decided"` // streams whose verdict came from this rule
}

// Stages of a pass that can settle a stream, as named in a Verdict.
const (
	StageRule    = "rule"    // an ordered rule matched
	StageDefault = "default" // no rule matched; the owner's default applied
	StageGroup   = "group"   // dropped by the group picker or group pattern
	StageType    = "type"    // dropped by the content type gate
	StagePattern = "pattern" // dropped by the type's include/exclude patterns
	StageQuality = "quality" // dropped because a better quality variant of the channel is kept
)

// Verdict is one stream's outcome in a pass: whether it survives and which
// stage settled that. A kept stream names the stage that let it through the
// rules (rule or default) since every later stage only narrows; a dropped
// stream names the stage that removed it. The preview's result browser lists
// these so an operator typing a pattern sees which streams it reaches.
type Verdict struct {
	Name  string            `json:"name"`
	Group string            `json:"group"`
	Type  types.ContentType `json:"type"`
	Kept  bool              `json:"kept"`
	Stage string            `json:"stage"`
	Rule  int               `json:"rule"` // 1-based position in the effective rule list when Stage is rule, else 0
}

// Report is the outcome of one pass over a source's catalog.
type Report struct {
	Total          int                             `json:"total"`
	Kept           int                             `json:"kept"`
	ByType         map[types.ContentType]*TypeStat `json:"byType"`
	Groups         []*GroupStat                    `json:"groups"`         // largest group first
	KeptSamples    []string                        `json:"keptSamples"`    // the first surviving stream names
	Rules          []RuleStat                      `json:"rules"`          // in evaluation order
	DefaultAction  string                          `json:"defaultAction"`  // verdict applied when no rule matched
	DefaultDecided int                             `json:"defaultDecided"` // streams settled by that verdict
	QualityDropped int                             `json:"qualityDropped"` // lower-quality variants removed because a better one is kept
	Verdicts       []Verdict                       `json:"-"`              // every stream's outcome, only with Options.Verdicts; paged by the caller
	groups         map[string]*GroupStat
	matched        []int // per-rule match counts, indexed like Rules
}

// startRules records the rule list a pass is about to evaluate, so the report
// names every rule even when nothing matches it.
func (r *Report) startRules(filter *CompiledFilter, withStats bool) {
	r.DefaultAction = filter.RuleDefault
	if len(filter.Rules) == 0 {
		return
	}
	r.Rules = make([]RuleStat, len(filter.Rules))
	for i, rule := range filter.Rules {
		r.Rules[i] = RuleStat{
			Index:   i + 1,
			Origin:  rule.origin,
			Field:   rule.rule.Field,
			Action:  rule.rule.Action,
			Pattern: rule.rule.Pattern,
			Note:    rule.rule.Note,
		}
	}
	if withStats {
		r.matched = make([]int, len(filter.Rules))
	}
}

// observeRules applies the ordered rule list to one stream and records which
// rule settled it, returning whether the rules keep it and the index of the
// deciding rule, or -1 when the default did.
func (r *Report) observeRules(filter *CompiledFilter, stream *types.Stream, group string, withStats bool) (bool, int) {
	if len(filter.Rules) == 0 {
		return filter.RuleDefault != config.FilterDefaultDrop, -1
	}

	keep, decidedBy, decided := ruleVerdict(filter.Rules, stream, group, withStats, r.matched)
	if !decided {
		r.DefaultDecided++
		return filter.RuleDefault != config.FilterDefaultDrop, -1
	}
	r.Rules[decidedBy].Decided++
	return keep, decidedBy
}

// newReport returns an empty report with every content type present, so a
// consumer can show a zero rather than a missing key.
func newReport() *Report {
	return &Report{
		Rules: []RuleStat{},
		ByType: map[types.ContentType]*TypeStat{
			types.ContentTypeLive:   {},
			types.ContentTypeVOD:    {},
			types.ContentTypeSeries: {},
		},
		Groups:      []*GroupStat{},
		KeptSamples: []string{},
		groups:      make(map[string]*GroupStat),
	}
}

// observe records one stream's verdict.
func (r *Report) observe(group, groupKey string, contentType types.ContentType, name string, kept bool) {
	r.Total++
	typeStat := r.ByType[contentType]
	if typeStat == nil {
		typeStat = &TypeStat{}
		r.ByType[contentType] = typeStat
	}
	typeStat.Total++

	groupStat := r.groups[groupKey]
	if groupStat == nil {
		groupStat = &GroupStat{Name: group, Samples: []string{}, typeCounts: make(map[types.ContentType]int)}
		r.groups[groupKey] = groupStat
		r.Groups = append(r.Groups, groupStat)
	}
	groupStat.Total++
	groupStat.typeCounts[contentType]++
	if len(groupStat.Samples) < maxGroupSamples {
		groupStat.Samples = append(groupStat.Samples, name)
	}

	if kept {
		r.Kept++
		typeStat.Kept++
		groupStat.Kept++
		if len(r.KeptSamples) < maxKeptSamples {
			r.KeptSamples = append(r.KeptSamples, name)
		}
	}
}

// finish settles each group's dominant type, folds in the per-rule match
// counts and orders the groups by size.
// Ties go to the type listed first here, live, since it is the type every
// unrecognised stream defaults to anyway.
func (r *Report) finish() {
	for _, groupStat := range r.Groups {
		best, bestCount := types.ContentTypeLive, -1
		for _, candidate := range []types.ContentType{types.ContentTypeLive, types.ContentTypeVOD, types.ContentTypeSeries} {
			if count := groupStat.typeCounts[candidate]; count > bestCount {
				best, bestCount = candidate, count
			}
		}
		groupStat.ContentType = best
	}
	for i := range r.matched {
		r.Rules[i].Matched = r.matched[i]
	}
	// without the extra pass a rule's only certain count is what it decided
	if r.matched == nil {
		for i := range r.Rules {
			r.Rules[i].Matched = r.Rules[i].Decided
		}
	}

	sort.SliceStable(r.Groups, func(i, j int) bool {
		if r.Groups[i].Total != r.Groups[j].Total {
			return r.Groups[i].Total > r.Groups[j].Total
		}
		return r.Groups[i].Name < r.Groups[j].Name
	})
}
