// work/filter/report.go
package filter

import (
	"sort"

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

// Report is the outcome of one pass over a source's catalog.
type Report struct {
	Total       int                             `json:"total"`
	Kept        int                             `json:"kept"`
	ByType      map[types.ContentType]*TypeStat `json:"byType"`
	Groups      []*GroupStat                    `json:"groups"`      // largest group first
	KeptSamples []string                        `json:"keptSamples"` // the first surviving stream names
	groups      map[string]*GroupStat
}

// newReport returns an empty report with every content type present, so a
// consumer can show a zero rather than a missing key.
func newReport() *Report {
	return &Report{
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

// finish settles each group's dominant type and orders the groups by size.
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
	sort.SliceStable(r.Groups, func(i, j int) bool {
		if r.Groups[i].Total != r.Groups[j].Total {
			return r.Groups[i].Total > r.Groups[j].Total
		}
		return r.Groups[i].Name < r.Groups[j].Name
	})
}
