// work/webui/webui.go
package webui

import (
	"io/fs"
	"net/http"
	"os"
	"sync"

	"kptv-proxy/work/logger"
)

// staticDirEnv names the environment variable that replaces the embedded
// assets with a directory on disk. It keeps the container layout (/static)
// serviceable and lets the admin UI be edited without rebuilding the binary.
const staticDirEnv = "KPTV_STATIC_DIR"

// fallbackVideoName is the offline clip, read from the root of the asset set.
const fallbackVideoName = "loading.ts"

var (
	// files is the asset set backing every request; the embedded FS unless
	// KPTV_STATIC_DIR redirects it at a directory.
	files fs.FS

	// fallback holds the offline clip. It is populated from the embedded copy
	// at Init, or read from files on first use when serving from disk.
	fallback     []byte
	fallbackOnce sync.Once
)

// Init selects the source of the web assets. The embedded arguments are used
// unless KPTV_STATIC_DIR names a directory, which then supplies both the admin
// interface files and the fallback clip. Call it before registering routes.
func Init(embedded fs.FS, embeddedFallback []byte) {
	if dir := os.Getenv(staticDirEnv); dir != "" {
		logger.Info("Serving web assets from %s", dir)
		files = os.DirFS(dir)
		return
	}
	files = embedded
	fallback = embeddedFallback
}

// Handler serves the asset set, for mounting under the /static/ prefix.
func Handler() http.Handler {
	return http.FileServerFS(files)
}

// ServeFile writes a single asset, named relative to the asset root
// ("admin.html", not "/static/admin.html").
func ServeFile(w http.ResponseWriter, r *http.Request, name string) {
	http.ServeFileFS(w, r, files, name)
}

// FallbackVideo returns the offline clip streamed when every source for a
// channel fails. The bytes are cached for the process lifetime, since the same
// clip is looped to every client. It returns nil if the clip is unavailable.
func FallbackVideo() []byte {
	fallbackOnce.Do(func() {
		if fallback != nil {
			return
		}
		data, err := fs.ReadFile(files, fallbackVideoName)
		if err != nil {
			logger.Error("{webui - FallbackVideo} Failed to read %s: %v", fallbackVideoName, err)
			return
		}
		fallback = data
	})
	return fallback
}
