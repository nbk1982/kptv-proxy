// work/filter/groups.go
package filter

import (
	"strings"

	"kptv-proxy/work/types"
)

// GroupOf returns the group label the engine reasons about, preferring
// group-title over tvg-group. It is the provider's raw value with the ends
// trimmed; use GroupKey to compare two labels.
func GroupOf(stream *types.Stream) string {
	for _, key := range groupAttributeKeys {
		if value := strings.TrimSpace(stream.Attributes[key]); value != "" {
			return value
		}
	}
	return ""
}

// groupSubject is the form of a group label the group regex is tested against:
// the same lowercased, trimmed shape every other pattern in the engine sees, so
// a pattern written for one field behaves the same on this one.
func groupSubject(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}
