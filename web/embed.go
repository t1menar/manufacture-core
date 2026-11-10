// Package web embeds the admin panel (the React SPA built into web/dist) and
// serves it over HTTP.
//
// Build the UI with `make -f web/web.mk web` (or `cd web && npm ci && npm run build`)
// before `go build`; without a build the handler serves a short placeholder page,
// so the Go code builds and tests without Node.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Built reports whether the embedded dist/ contains a built UI (index.html).
func Built() bool {
	_, err := fs.Stat(dist, "dist/index.html")
	return err == nil
}

// Handler serves the admin panel: static files from dist/, long-lived cache for
// hashed /assets/*, and index.html (no-cache) for every other GET path so the
// client-side router can handle deep links. Paths under /api/ are never answered
// with index.html (they get 404) to keep API mistakes visible.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil { // only if the embed pattern changes
		panic(err)
	}
	return newHandler(sub)
}

func newHandler(root fs.FS) http.Handler {
	files := http.FileServerFS(root)
	index, indexErr := fs.ReadFile(root, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if indexErr != nil {
			servePlaceholder(w)
			return
		}
		p := path.Clean("/" + r.URL.Path)
		if p == "/api" || strings.HasPrefix(p, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(p, "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(root, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(index)
		}
	})
}

const placeholder = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Manufacture</title></head>
<body style="font-family:system-ui,sans-serif;background:#16181d;color:#e6e6e6;padding:3rem">
<h1>Manufacture</h1>
<p>The admin panel was not built into this binary.</p>
<p>Run <code>make -f web/web.mk web</code> (or <code>cd web &amp;&amp; npm ci &amp;&amp; npm run build</code>) and rebuild. The API is at <a style="color:#7cc4ff" href="/api/v1/system">/api/v1</a>.</p>
</body></html>
`

func servePlaceholder(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(placeholder))
}
