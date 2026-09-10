// work/parser/rawcache.go
package parser

import (
	"fmt"

	"kptv-proxy/work/config"
)

// IsXCSource reports whether a source is imported through the Xtream Codes API
// rather than fetched as an M3U playlist. Credentials are what tell them apart.
func IsXCSource(source *config.SourceConfig) bool {
	return source.Username != "" && source.Password != ""
}

// RawCacheKey is the cache key under which a source's unfiltered catalog is
// stored between imports. It is shared so the admin layer can invalidate a
// single source without knowing which parser produced the entry.
func RawCacheKey(source *config.SourceConfig) string {
	if IsXCSource(source) {
		return fmt.Sprintf("xc:v2:%s:%s:%s", source.URL, source.Username, source.Password)
	}
	return fmt.Sprintf("m3u8:%s", source.URL)
}
