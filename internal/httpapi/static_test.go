package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestStaticHandlerCacheControl(t *testing.T) {
	server := &Server{staticFS: fstest.MapFS{
		"index.html":              {Data: []byte(`<!doctype html><script type="module" src="/assets/app-abc123.js"></script>`)},
		"assets/app-abc123.js":    {Data: []byte(`console.log("ok")`)},
		"assets/index-def456.css": {Data: []byte(`body{}`)},
		"manifest.webmanifest":    {Data: []byte(`{}`)},
	}}
	handler := server.staticHandler()

	cases := []struct {
		path string
		want string
	}{
		{"/", "no-cache"},
		{"/stockv2", "no-cache"},
		{"/assets/app-abc123.js", "public, max-age=31536000, immutable"},
		{"/assets/index-def456.css", "public, max-age=31536000, immutable"},
		{"/manifest.webmanifest", "no-cache"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if got := rec.Header().Get("Cache-Control"); got != tc.want {
			t.Fatalf("%s Cache-Control = %q, want %q", tc.path, got, tc.want)
		}
	}
}
