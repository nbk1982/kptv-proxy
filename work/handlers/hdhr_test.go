package handlers

import (
	"crypto/tls"
	"encoding/json"
	"kptv-proxy/work/config"
	"kptv-proxy/work/proxy"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testPublicBase = "http://iptv.example.com:8080"

func TestHDHRBaseURL(t *testing.T) {
	cases := []struct {
		name  string
		host  string
		proto string
		tls   bool
		want  string
	}{
		{"loopback host as used by a same-machine Plex", "127.0.0.1:8080", "", false, "http://127.0.0.1:8080"},
		{"lan host", "192.168.1.10:8080", "", false, "http://192.168.1.10:8080"},
		{"https via reverse proxy header", "iptv.example.com", "https", false, "https://iptv.example.com"},
		{"direct tls", "iptv.example.com", "", true, "https://iptv.example.com"},
		{"no host falls back to configured base", "", "", false, testPublicBase},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/discover.json", nil)
			r.Host = tc.host
			if tc.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if got := hdhrBaseURL(r, testPublicBase+"/"); got != tc.want {
				t.Fatalf("hdhrBaseURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHDHRDiscoverUsesRequestHost covers the same-machine Plex scenario: the
// tuner is added as 127.0.0.1:8080, so the announced BaseURL/LineupURL must
// point back at loopback, while DeviceID stays tied to the configured base URL.
func TestHDHRDiscoverUsesRequestHost(t *testing.T) {
	sp := &proxy.StreamProxy{Config: &config.Config{BaseURL: testPublicBase}}

	r := httptest.NewRequest(http.MethodGet, "/discover.json", nil)
	r.Host = "127.0.0.1:8080"
	w := httptest.NewRecorder()
	handleHDHRDiscover(sp)(w, r)

	var got hdhrDiscoverResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode discover.json: %v", err)
	}
	if got.BaseURL != "http://127.0.0.1:8080" {
		t.Errorf("BaseURL = %q, want loopback", got.BaseURL)
	}
	if got.LineupURL != "http://127.0.0.1:8080/lineup.json" {
		t.Errorf("LineupURL = %q, want loopback lineup", got.LineupURL)
	}
	if got.DeviceID != HDHRDeviceID(testPublicBase) {
		t.Errorf("DeviceID = %q, want value derived from configured base URL", got.DeviceID)
	}
}
