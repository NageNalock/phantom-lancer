package images

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"testing"
)

func TestParseMediaMultipartRequestAllowsKindedMediaAsset(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("mode", ModeImageToImage); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("source_asset_1", "media:medasset_abcdefgh"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/images/generations", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	got, err := ParseMediaMultipartRequest(req, string(MediaTypeImage), ModeImageToImage)
	if err != nil {
		t.Fatalf("parse media multipart request: %v", err)
	}
	if len(got.Images) != 1 {
		t.Fatalf("expected one source image, got %d", len(got.Images))
	}
	if got.Images[0].URL != "asset:media:medasset_abcdefgh" {
		t.Fatalf("unexpected asset URL: %q", got.Images[0].URL)
	}
}
