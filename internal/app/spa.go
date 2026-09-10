package app

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/ui"
)

// spaHandler serves the embedded UI (REQ-CORE-010). Unknown non-API paths return
// index.html. Hashed assets are immutable. index.html is no-cache.
func spaHandler() http.Handler {
	dist, err := fs.Sub(ui.Dist, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			httpx.WriteError(w, r, httpx.Errorf(http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed"))
			return
		}
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(dist, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			httpx.WriteError(w, r, httpx.Errorf(http.StatusServiceUnavailable, "ui_not_built", "the UI is not in this build"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
