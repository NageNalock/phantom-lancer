package images

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const onePixelPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="

func TestValidateRequestModes(t *testing.T) {
	tests := []struct {
		name    string
		request ImagineRequest
		wantErr bool
	}{
		{
			name: "text to image",
			request: ImagineRequest{
				Mode:   ModeTextToImage,
				Prompt: "A quiet control plane workstation",
				Model:  "grok-imagine-image-quality",
			},
		},
		{
			name: "image to image requires one source",
			request: ImagineRequest{
				Mode:   ModeImageToImage,
				Prompt: "Turn this into a product render",
				Model:  "grok-imagine-image-quality",
			},
			wantErr: true,
		},
		{
			name: "multi image accepts three sources",
			request: ImagineRequest{
				Mode:   ModeMultiImageEdit,
				Prompt: "Merge these references",
				Model:  "grok-imagine-image-quality",
				Images: []ImageInput{
					{URL: "https://example.com/a.png"},
					{URL: "https://example.com/b.png"},
					{URL: "https://example.com/c.png"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRequest(tt.request)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRequestPayloadEndpoints(t *testing.T) {
	endpoint, payload, err := RequestPayload(ImagineRequest{
		Mode:           ModeImageToImage,
		Prompt:         "Edit this image",
		Model:          "grok-imagine-image-quality",
		AspectRatio:    "1:1",
		Resolution:     "2k",
		ResponseFormat: "url",
		N:              1,
		Images:         []ImageInput{{URL: "https://example.com/source.png"}},
	})
	if err != nil {
		t.Fatalf("RequestPayload: %v", err)
	}
	if endpoint != "/images/edits" {
		t.Fatalf("endpoint = %q", endpoint)
	}
	if payload["image"] == nil {
		t.Fatal("missing image payload")
	}
	if payload["aspect_ratio"] != "1:1" || payload["resolution"] != "2k" {
		t.Fatalf("missing options: %#v", payload)
	}
}

func TestAssetStoreStoresDataURL(t *testing.T) {
	dir := t.TempDir()
	store := NewAssetStore(filepath.Join(dir, "generated"), nil)
	result := store.StoreResultImages(context.Background(), "imgjob_test", []ResultImage{{
		DataURL:  onePixelPNG,
		MimeType: "image/png",
	}})
	if result.StoreFailures != 0 {
		t.Fatalf("store failures = %d", result.StoreFailures)
	}
	if len(result.Outputs) != 1 || result.Outputs[0].Storage != "local" {
		t.Fatalf("unexpected outputs: %#v", result.Outputs)
	}
	if _, err := os.Stat(filepath.Join(dir, "generated", result.Outputs[0].LocalName)); err != nil {
		t.Fatalf("stored image missing: %v", err)
	}
}
