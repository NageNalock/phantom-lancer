package images

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestImageBytesRetriesBrowserHeadersAfterForbidden(t *testing.T) {
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !strings.Contains(r.UserAgent(), "Mozilla/5.0") {
			http.Error(w, "missing browser user agent", http.StatusForbidden)
			return
		}
		if !strings.Contains(r.Header.Get("Accept"), "image/") {
			http.Error(w, "missing image accept", http.StatusForbidden)
			return
		}
		if got, want := r.Header.Get("Referer"), server.URL+"/"; got != want {
			http.Error(w, "missing origin referer", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer server.Close()

	store := NewAssetStore(t.TempDir(), server.Client())
	data, mimeType, err := store.ImageBytes(context.Background(), ResultImage{URL: server.URL + "/image.png?token=example"})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
	if !bytes.Equal(data, png) {
		t.Fatal("image bytes mismatch")
	}
	if mimeType != "image/png" {
		t.Fatalf("mimeType = %q, want image/png", mimeType)
	}
}
