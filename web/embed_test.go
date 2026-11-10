package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func serve(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, http.NoBody))
	return rec
}

func TestHandlerSPA(t *testing.T) {
	root := fstest.MapFS{
		"index.html":         {Data: []byte("<html>app</html>")},
		"favicon.svg":        {Data: []byte("<svg/>")},
		"assets/index-ab.js": {Data: []byte("console.log(1)")},
	}
	h := newHandler(root)

	tests := []struct {
		name, method, path string
		code               int
		body, cache        string
	}{
		{"root", http.MethodGet, "/", http.StatusOK, "app", "no-cache"},
		{"deep link", http.MethodGet, "/runs/123", http.StatusOK, "app", "no-cache"},
		{"index", http.MethodGet, "/index.html", http.StatusOK, "app", "no-cache"},
		{"asset", http.MethodGet, "/assets/index-ab.js", http.StatusOK, "console.log", "immutable"},
		{"public file", http.MethodGet, "/favicon.svg", http.StatusOK, "<svg/>", "no-cache"},
		{"missing asset", http.MethodGet, "/assets/nope.js", http.StatusNotFound, "", ""},
		{"missing file", http.MethodGet, "/robots.txt", http.StatusNotFound, "", ""},
		{"api", http.MethodGet, "/api/v1/nope", http.StatusNotFound, "", ""},
		{"traversal stays in root", http.MethodGet, "/../../etc/passwd", http.StatusOK, "app", "no-cache"},
		{"head", http.MethodHead, "/pipelines", http.StatusOK, "", "no-cache"},
		{"post", http.MethodPost, "/", http.StatusMethodNotAllowed, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(t, h, tc.method, tc.path)
			if rec.Code != tc.code {
				t.Fatalf("%s %s: code %d, want %d", tc.method, tc.path, rec.Code, tc.code)
			}
			if tc.body != "" && !strings.Contains(rec.Body.String(), tc.body) {
				t.Fatalf("body %q does not contain %q", rec.Body.String(), tc.body)
			}
			if tc.cache != "" && !strings.Contains(rec.Header().Get("Cache-Control"), tc.cache) {
				t.Fatalf("Cache-Control %q, want %q", rec.Header().Get("Cache-Control"), tc.cache)
			}
		})
	}
}

func TestHandlerPlaceholder(t *testing.T) {
	h := newHandler(fstest.MapFS{".keep": {Data: nil}})
	rec := serve(t, h, http.MethodGet, "/anything")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "not built") {
		t.Fatalf("placeholder: code %d body %q", rec.Code, rec.Body.String())
	}
}

func TestHandlerEmbedded(t *testing.T) {
	rec := serve(t, Handler(), http.MethodGet, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	if Built() != !strings.Contains(rec.Body.String(), "not built") {
		t.Fatalf("Built()=%v disagrees with the served page", Built())
	}
}
