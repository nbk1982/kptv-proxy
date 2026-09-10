package parser

import (
	"testing"

	"kptv-proxy/work/config"
	"kptv-proxy/work/types"
)

func TestRawCacheKeyDistinguishesXCFromM3U(t *testing.T) {
	m3u := &config.SourceConfig{URL: "http://p/list.m3u"}
	xc := &config.SourceConfig{URL: "http://p", Username: "u", Password: "s"}
	if IsXCSource(m3u) || !IsXCSource(xc) {
		t.Fatal("credentials are what mark an XC source")
	}
	if RawCacheKey(m3u) == RawCacheKey(xc) {
		t.Fatal("the two parsers must not share a cache entry")
	}
	// a credential change has to be a different entry, not a stale hit
	other := &config.SourceConfig{URL: "http://p", Username: "u", Password: "rotated"}
	if RawCacheKey(xc) == RawCacheKey(other) {
		t.Fatal("rotating the password must invalidate the cached catalog")
	}
}

func TestAdoptSourceRepointsCachedStreams(t *testing.T) {
	// a catalog restored from the raw cache carries a SourceConfig that came
	// back out of JSON; playback reads headers and the connection counter
	// through stream.Source, so it has to be the live one
	detached := &config.SourceConfig{Name: "copy", URL: "http://p/list.m3u"}
	live := &config.SourceConfig{Name: "live", URL: "http://p/list.m3u", MaxConnections: 5}
	streams := []*types.Stream{
		{Name: "A", Source: detached},
		{Name: "B", Source: nil},
	}

	got := adoptSource(streams, live)
	if len(got) != 2 {
		t.Fatalf("every stream must come back, got %d", len(got))
	}
	for _, s := range got {
		if s.Source != live {
			t.Fatalf("stream %s still points at %+v", s.Name, s.Source)
		}
	}
	live.ActiveConns.Add(1)
	if got[0].Source.ActiveConns.Load() != 1 {
		t.Fatal("the stream must observe the live source's connection counter")
	}
}
