package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticHandlerCacheControl(t *testing.T) {
	server := &Server{staticFS: fstest.MapFS{
		"index.html":              {Data: []byte(`<!doctype html><script type="module" src="/assets/app-abc123.js"></script>`)},
		"assets/app-abc123.js":    {Data: []byte(`console.log("ok")`)},
		"assets/app-abc123.js.br": {Data: []byte("brotli-data")},
		"assets/app-abc123.js.gz": {Data: []byte("gzip-data")},
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

func TestStaticHandlerNegotiatesPrecompressedAssets(t *testing.T) {
	server := &Server{staticFS: fstest.MapFS{
		"index.html":              {Data: []byte("index")},
		"assets/app-abc123.js":    {Data: []byte("identity-data")},
		"assets/app-abc123.js.br": {Data: []byte("brotli-data")},
		"assets/app-abc123.js.gz": {Data: []byte("gzip-data")},
	}}
	handler := server.staticHandler()

	cases := []struct {
		name           string
		acceptEncoding string
		wantEncoding   string
		wantBody       string
	}{
		{name: "brotli preferred at equal quality", acceptEncoding: "gzip, br", wantEncoding: "br", wantBody: "brotli-data"},
		{name: "quality selects gzip", acceptEncoding: "br;q=0, gzip;q=0.8", wantEncoding: "gzip", wantBody: "gzip-data"},
		{name: "wildcard supports available encoding", acceptEncoding: "*;q=0.5", wantEncoding: "br", wantBody: "brotli-data"},
		{name: "identity without negotiation", wantBody: "identity-data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/assets/app-abc123.js", nil)
			req.Header.Set("Accept-Encoding", tc.acceptEncoding)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			response := rec.Result()
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if got := response.Header.Get("Content-Encoding"); got != tc.wantEncoding {
				t.Fatalf("Content-Encoding = %q, want %q", got, tc.wantEncoding)
			}
			if got := string(body); got != tc.wantBody {
				t.Fatalf("body = %q, want %q", got, tc.wantBody)
			}
			if got := response.Header.Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
				t.Fatalf("Vary = %q, want Accept-Encoding", got)
			}
			if got := response.Header.Get("Content-Type"); !strings.Contains(got, "javascript") {
				t.Fatalf("Content-Type = %q, want javascript", got)
			}
		})
	}
}

func TestAcceptedEncodingQuality(t *testing.T) {
	cases := []struct {
		header string
		name   string
		want   float64
	}{
		{header: "br;q=0.7, gzip;q=1", name: "br", want: 0.7},
		{header: "br;q=0.7, gzip;q=1", name: "gzip", want: 1},
		{header: "*;q=0.3", name: "br", want: 0.3},
		{header: "br;q=invalid", name: "br", want: 0},
		{header: "gzip", name: "br", want: 0},
	}
	for _, tc := range cases {
		if got := acceptedEncodingQuality(tc.header, tc.name); got != tc.want {
			t.Fatalf("acceptedEncodingQuality(%q, %q) = %v, want %v", tc.header, tc.name, got, tc.want)
		}
	}
}
