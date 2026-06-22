package images

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/storage"
)

const onePixelPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="

func TestValidateImageURLKeepsRemoteLimitButAllowsDataURLBytes(t *testing.T) {
	if err := ValidateImageURL("https://example.com/" + strings.Repeat("a", 4096)); err == nil {
		t.Fatal("long remote URL should fail")
	}
	largeDataURL := "data:image/png;base64," + strings.Repeat("A", 4096)
	if err := ValidateImageURL(largeDataURL); err != nil {
		t.Fatalf("large data URL should be accepted by byte limit: %v", err)
	}
	tooLargeDataURL := "data:image/png;base64," + strings.Repeat("A", base64.StdEncoding.EncodedLen(MaxImageBytes)+4)
	if err := ValidateImageURL(tooLargeDataURL); err == nil {
		t.Fatal("oversized data URL should fail")
	}
}

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
	ctx := context.Background()
	dir := t.TempDir()
	store := NewAssetStore(filepath.Join(dir, "generated"), nil)
	output, ok := store.storeImage(ctx, "imgjob_test", 0, ResultImage{
		DataURL:  onePixelPNG,
		MimeType: "image/png",
	})
	if !ok {
		t.Fatal("storeImage failed for data URL")
	}
	if output.Storage != "local" {
		t.Fatalf("unexpected output storage: %#v", output)
	}
	if _, err := os.Stat(filepath.Join(dir, "generated", output.LocalName)); err != nil {
		t.Fatalf("stored image missing: %v", err)
	}
}

func TestResponseFormatForStorageForcesBytesWhenObjectStorageEnabled(t *testing.T) {
	if got := responseFormatForStorage("url", storage.ImageStorageSettings{Backend: "s3"}); got != "b64_json" {
		t.Fatalf("s3 response format = %q, want b64_json", got)
	}
	if got := responseFormatForStorage("url", storage.ImageStorageSettings{Backend: "object_storage", ObjectStorageProfileID: "obj_123"}); got != "b64_json" {
		t.Fatalf("object_storage response format = %q, want b64_json", got)
	}
	if got := responseFormatForStorage("url", storage.ImageStorageSettings{Backend: "local"}); got != "url" {
		t.Fatalf("local response format = %q, want url", got)
	}
}

func TestGeneratedRemoteAssetCreatedWhenFetchFails(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "expired", http.StatusForbidden)
	}))
	defer remote.Close()

	job, err := db.CreateImageGenerationJob(ctx, storage.ImageGenerationJob{
		Provider:   "xai",
		Status:     "running",
		Mode:       ModeTextToImage,
		ModeLabel:  ModeLabel(ModeTextToImage),
		Model:      "grok-imagine-image-quality",
		Prompt:     "quiet generated image",
		ImageCount: 1,
	}, nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	service := &Service{
		Store:  db,
		Hub:    events.NewHub(),
		Assets: NewAssetStore(filepath.Join(t.TempDir(), "images"), nil),
	}
	remoteURL := remote.URL + "/image.png?signature=secret"
	outputs, failures := service.storeGeneratedAssets(ctx, job, ImagineRequest{Prompt: job.Prompt}, &ImagineResult{
		Images: []ResultImage{{
			URL:      remoteURL,
			MimeType: "image/png",
		}},
	})
	if failures != 1 {
		t.Fatalf("failures = %d, want 1", failures)
	}
	if len(outputs) != 1 || outputs[0].AssetID == "" || outputs[0].Storage != "remote" {
		t.Fatalf("output should be linked to a remote asset: %#v", outputs)
	}
	if _, err := db.CompleteImageGenerationJob(ctx, job.ID, "/images/generations", map[string]any{}, outputs); err != nil {
		t.Fatalf("complete job: %v", err)
	}

	assets, err := db.ListImageAssets(ctx, 20, "", "", "", "", "")
	if err != nil {
		t.Fatalf("list assets: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("assets count = %d, want 1: %#v", len(assets), assets)
	}
	if assets[0].ID != outputs[0].AssetID || assets[0].StorageBackend != "remote" {
		t.Fatalf("unexpected library asset: %#v", assets[0])
	}
	if assets[0].URL != remoteURL {
		t.Fatalf("remote library asset URL = %q, want %q", assets[0].URL, remoteURL)
	}
	gotJob, err := db.GetImageGenerationJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if len(gotJob.Outputs) != 1 || gotJob.Outputs[0].AssetID != assets[0].ID {
		t.Fatalf("history output should reference library asset: %#v", gotJob.Outputs)
	}
	if gotJob.Outputs[0].URL != remoteURL {
		t.Fatalf("remote history output URL = %q, want %q", gotJob.Outputs[0].URL, remoteURL)
	}
}

func TestStoreMediaGeneratedImagesCreatesMediaAsset(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	job, err := db.CreateMediaGenerationJob(ctx, storage.MediaGenerationJob{
		MediaType:  string(MediaTypeImage),
		Provider:   string(ProviderAgnes),
		Status:     "running",
		Mode:       ModeTextToImage,
		ModeLabel:  ModeLabel(ModeTextToImage),
		Model:      "agnes-image-2.1-flash",
		Prompt:     "quiet media image",
		Parameters: map[string]any{"n": 1},
	}, nil)
	if err != nil {
		t.Fatalf("create media job: %v", err)
	}
	service := &Service{
		Store:  db,
		Hub:    events.NewHub(),
		Assets: NewAssetStore(filepath.Join(t.TempDir(), "media"), nil),
	}
	outputs, failures := service.storeMediaGeneratedImages(ctx, job, ImagineRequest{Prompt: job.Prompt}, &ImagineResult{
		Images: []ResultImage{{
			DataURL:  onePixelPNG,
			MimeType: "image/png",
		}},
	})
	if failures != 0 {
		t.Fatalf("failures = %d, want 0", failures)
	}
	if len(outputs) != 1 || outputs[0].AssetID == "" || outputs[0].Storage == "remote" {
		t.Fatalf("output should reference a stored media asset: %#v", outputs)
	}
	assets, err := db.ListMediaAssets(ctx, 20, "", "", "", "", false)
	if err != nil {
		t.Fatalf("list media assets: %v", err)
	}
	if len(assets) != 1 || assets[0].ID != outputs[0].AssetID || assets[0].Provider != string(ProviderAgnes) {
		t.Fatalf("unexpected media assets: %#v", assets)
	}
}

func TestStoreMediaGeneratedImagesDoesNotExposeUnstoredOutput(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	job, err := db.CreateMediaGenerationJob(ctx, storage.MediaGenerationJob{
		MediaType:  string(MediaTypeImage),
		Provider:   string(ProviderAgnes),
		Status:     "running",
		Mode:       ModeTextToImage,
		ModeLabel:  ModeLabel(ModeTextToImage),
		Model:      "agnes-image-2.1-flash",
		Prompt:     "quiet media image",
		Parameters: map[string]any{"n": 1},
	}, nil)
	if err != nil {
		t.Fatalf("create media job: %v", err)
	}
	blockedDir := filepath.Join(t.TempDir(), "media-file")
	if err := os.WriteFile(blockedDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create blocking file: %v", err)
	}
	service := &Service{
		Store:  db,
		Hub:    events.NewHub(),
		Assets: NewAssetStore(blockedDir, nil),
	}
	outputs, failures := service.storeMediaGeneratedImages(ctx, job, ImagineRequest{Prompt: job.Prompt}, &ImagineResult{
		Images: []ResultImage{{
			DataURL:  onePixelPNG,
			MimeType: "image/png",
		}},
	})
	if failures != 1 {
		t.Fatalf("failures = %d, want 1", failures)
	}
	if len(outputs) != 0 {
		t.Fatalf("unstored outputs should not be exposed as success outputs: %#v", outputs)
	}
	assets, err := db.ListMediaAssets(ctx, 20, "", "", "", "", false)
	if err != nil {
		t.Fatalf("list media assets: %v", err)
	}
	if len(assets) != 0 {
		t.Fatalf("default media assets should hide failed records: %#v", assets)
	}
	failedAssets, err := db.ListMediaAssets(ctx, 20, "", "", "", "failed", false)
	if err != nil {
		t.Fatalf("list failed media assets: %v", err)
	}
	if len(failedAssets) != 1 || failedAssets[0].Status != "failed" || failedAssets[0].LastError == "" {
		t.Fatalf("failed media asset should retain diagnostics: %#v", failedAssets)
	}
	if hasStoredMediaOutputs(outputs) {
		t.Fatalf("unstored outputs should not count as stored: %#v", outputs)
	}
}

func TestFinalizeVideoJobFailsWithoutStoredAsset(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	job, err := db.CreateMediaGenerationJob(ctx, storage.MediaGenerationJob{
		MediaType:  string(MediaTypeVideo),
		Provider:   string(ProviderAgnes),
		Status:     "running",
		Mode:       VideoModeTextToVideo,
		ModeLabel:  ModeLabel(VideoModeTextToVideo),
		Model:      "agnes-video-v2.0",
		Prompt:     "quiet media video",
		Parameters: map[string]any{"seconds": 5},
	}, nil)
	if err != nil {
		t.Fatalf("create media job: %v", err)
	}
	service := &Service{
		Store:  db,
		Hub:    events.NewHub(),
		Assets: NewAssetStore(filepath.Join(t.TempDir(), "media"), nil),
	}

	service.finalizeVideoJob(ctx, job, &VideoPollResult{
		Status:   "completed",
		VideoURL: "://invalid-video-url",
		Seconds:  5,
	}, "")

	got, err := db.GetMediaGenerationJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get media job: %v", err)
	}
	if got.Status != "failed" || got.ErrorMessage == "" {
		t.Fatalf("video job should fail when no stored asset exists: %#v", got)
	}
	if len(got.Outputs) != 0 {
		t.Fatalf("failed video job should not expose remote-only outputs: %#v", got.Outputs)
	}
	assets, err := db.ListMediaAssets(ctx, 20, string(MediaTypeVideo), "", "", "", false)
	if err != nil {
		t.Fatalf("list media assets: %v", err)
	}
	if len(assets) != 0 {
		t.Fatalf("video library should stay empty without stored assets: %#v", assets)
	}
}

func TestUploadLibraryAssetDeduplicatesByChecksum(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	data, mimeType, err := DecodeTestDataURL(onePixelPNG)
	if err != nil {
		t.Fatalf("decode test image: %v", err)
	}
	service := &Service{
		Store:  db,
		Hub:    events.NewHub(),
		Assets: NewAssetStore(filepath.Join(t.TempDir(), "images"), nil),
	}
	first, err := service.UploadLibraryAsset(ctx, "first.png", data, mimeType)
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	if first.Duplicate {
		t.Fatal("first upload should not be duplicate")
	}
	second, err := service.UploadLibraryAsset(ctx, "second.png", data, mimeType)
	if err != nil {
		t.Fatalf("second upload: %v", err)
	}
	if !second.Duplicate || second.Asset.ID != first.Asset.ID {
		t.Fatalf("second upload should reuse first asset: first=%#v second=%#v", first, second)
	}
	assets, err := db.ListImageAssets(ctx, 20, "", "", "", "", "")
	if err != nil {
		t.Fatalf("list assets: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("asset count = %d, want 1", len(assets))
	}
}

func TestResolveLibraryImageInputForImageToImage(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	data, mimeType, err := DecodeTestDataURL(onePixelPNG)
	if err != nil {
		t.Fatalf("decode test image: %v", err)
	}
	service := &Service{
		Store:  db,
		Hub:    events.NewHub(),
		Assets: NewAssetStore(filepath.Join(t.TempDir(), "images"), nil),
	}
	uploaded, err := service.UploadLibraryAsset(ctx, "source.png", data, mimeType)
	if err != nil {
		t.Fatalf("upload source: %v", err)
	}
	resolved, err := service.resolveLibraryImageInputs(ctx, ImagineRequest{
		Mode:   ModeImageToImage,
		Prompt: "edit",
		Images: []ImageInput{{
			URL:        "asset:" + uploaded.Asset.ID,
			SourceType: "library_asset",
		}},
	})
	if err != nil {
		t.Fatalf("resolve library input: %v", err)
	}
	if len(resolved.Images) != 1 || !strings.HasPrefix(resolved.Images[0].URL, "data:image/png;base64,") {
		t.Fatalf("library image should resolve to data URL: %#v", resolved.Images)
	}
}

func DecodeTestDataURL(dataURL string) ([]byte, string, error) {
	return NewAssetStore("", nil).DecodeDataURL(dataURL)
}
