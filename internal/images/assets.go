package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"phantom-lancer/internal/storage"
)

const maxStoredImageBytes = 32 << 20
const remoteImageBrowserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var safeAssetNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type AssetStore struct {
	dir  string
	http *http.Client
}

type StoredOutputs struct {
	Outputs       []storage.ImageGenerationOutput
	StoreFailures int
}

type StoredAssetData struct {
	LocalName string
	MimeType  string
	SizeBytes int64
	Width     int
	Height    int
	Checksum  string
}

func NewAssetStore(dir string, httpClient *http.Client) *AssetStore {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &AssetStore{dir: dir, http: httpClient}
}

func (s *AssetStore) ImageBytes(ctx context.Context, image ResultImage) ([]byte, string, error) {
	return s.imageBytes(ctx, image)
}

func (s *AssetStore) DecodeDataURL(dataURL string) ([]byte, string, error) {
	return decodeImageDataURL(dataURL)
}

func (s *AssetStore) StoreBytes(assetID string, data []byte, mimeType string) (StoredAssetData, error) {
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	if !AllowedImageMime(mimeType) {
		return StoredAssetData{}, errors.New("image mime type is unsupported")
	}
	if len(data) > maxStoredImageBytes {
		return StoredAssetData{}, errors.New("image data is too large")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return StoredAssetData{}, err
	}
	localName := fmt.Sprintf("%s%s", assetID, imageExt(mimeType))
	fullPath, ok := s.AssetPath(localName)
	if !ok {
		return StoredAssetData{}, errors.New("asset path is invalid")
	}
	if err := os.WriteFile(fullPath, data, 0o600); err != nil {
		return StoredAssetData{}, err
	}
	info := ImageInfo(data, mimeType)
	info.LocalName = localName
	return info, nil
}

func ImageInfo(data []byte, mimeType string) StoredAssetData {
	width, height := imageDimensions(data)
	sum := sha256.Sum256(data)
	return StoredAssetData{
		MimeType:  mimeType,
		SizeBytes: int64(len(data)),
		Width:     width,
		Height:    height,
		Checksum:  hex.EncodeToString(sum[:]),
	}
}

func (s *AssetStore) ReadLocal(name string) (string, []byte, error) {
	path, ok := s.AssetPath(name)
	if !ok {
		return "", nil, errors.New("asset name is invalid")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	mimeType := http.DetectContentType(data)
	return mimeType, data, nil
}

func (s *AssetStore) AssetPath(name string) (string, bool) {
	if !safeAssetNamePattern.MatchString(name) {
		return "", false
	}
	fullPath := filepath.Join(s.dir, name)
	cleanDir, err := filepath.Abs(s.dir)
	if err != nil {
		return "", false
	}
	cleanPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", false
	}
	if filepath.Dir(cleanPath) != cleanDir {
		return "", false
	}
	return cleanPath, true
}

func (s *AssetStore) Remove(names []string) {
	for _, name := range names {
		if path, ok := s.AssetPath(name); ok {
			_ = os.Remove(path)
		}
	}
}

func (s *AssetStore) storeImage(ctx context.Context, jobID string, index int, image ResultImage) (storage.ImageGenerationOutput, bool) {
	output := storage.ImageGenerationOutput{
		Slot:          index + 1,
		RemoteURL:     image.URL,
		MimeType:      image.MimeType,
		RevisedPrompt: image.RevisedPrompt,
		Storage:       "remote",
	}
	if image.URL != "" {
		output.URL = image.URL
	}

	data, mimeType, err := s.imageBytes(ctx, image)
	if err != nil {
		return output, false
	}
	if mimeType == "" {
		mimeType = image.MimeType
	}
	if !AllowedImageMime(mimeType) {
		return output, false
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return output, false
	}
	localName := fmt.Sprintf("%s-%02d%s", jobID, index+1, imageExt(mimeType))
	fullPath, ok := s.AssetPath(localName)
	if !ok {
		return output, false
	}
	if err := os.WriteFile(fullPath, data, 0o600); err != nil {
		return output, false
	}
	output.LocalName = localName
	output.URL = "/api/images/assets/" + localName
	output.MimeType = mimeType
	output.Storage = "local"
	output.SizeBytes = int64(len(data))
	return output, true
}

func (s *AssetStore) imageBytes(ctx context.Context, image ResultImage) ([]byte, string, error) {
	if image.DataURL != "" {
		return decodeImageDataURL(image.DataURL)
	}
	if image.URL == "" {
		return nil, "", errors.New("image has no url")
	}
	parsed, err := url.Parse(image.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, "", errors.New("remote image url is invalid")
	}
	data, mimeType, statusCode, err := s.fetchRemoteImage(ctx, image.URL, parsed, false)
	if err == nil {
		return data, mimeType, nil
	}
	if !shouldRetryRemoteImageWithBrowserHeaders(statusCode) {
		return nil, "", err
	}
	data, mimeType, _, retryErr := s.fetchRemoteImage(ctx, image.URL, parsed, true)
	if retryErr != nil {
		return nil, "", retryErr
	}
	return data, mimeType, nil
}

func (s *AssetStore) fetchRemoteImage(ctx context.Context, rawURL string, parsed *url.URL, browserHeaders bool) ([]byte, string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", 0, err
	}
	if browserHeaders {
		applyRemoteImageBrowserHeaders(req, parsed)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", resp.StatusCode, fmt.Errorf("remote image returned %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxStoredImageBytes+1))
	if err != nil {
		return nil, "", resp.StatusCode, err
	}
	if len(data) > maxStoredImageBytes {
		return nil, "", resp.StatusCode, errors.New("remote image is too large")
	}
	mimeType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if mimeType == "" || !AllowedImageMime(mimeType) {
		mimeType = http.DetectContentType(data)
	}
	if !AllowedImageMime(mimeType) {
		return nil, "", resp.StatusCode, errors.New("remote image mime type is unsupported")
	}
	return data, mimeType, resp.StatusCode, nil
}

func shouldRetryRemoteImageWithBrowserHeaders(statusCode int) bool {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotAcceptable:
		return true
	default:
		return false
	}
}

func applyRemoteImageBrowserHeaders(req *http.Request, parsed *url.URL) {
	req.Header.Set("User-Agent", remoteImageBrowserUserAgent)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/png,image/jpeg,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-Fetch-Dest", "image")
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if referer := remoteImageOriginReferer(parsed); referer != "" {
		req.Header.Set("Referer", referer)
	}
}

func remoteImageOriginReferer(parsed *url.URL) string {
	if parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host + "/"
}

func decodeImageDataURL(dataURL string) ([]byte, string, error) {
	if !strings.HasPrefix(dataURL, "data:image/") {
		return nil, "", errors.New("data url is not an image")
	}
	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 || !strings.Contains(parts[0], ";base64") {
		return nil, "", errors.New("data url is not base64 encoded")
	}
	mimeType := strings.TrimPrefix(strings.TrimSuffix(parts[0], ";base64"), "data:")
	data, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxStoredImageBytes {
		return nil, "", errors.New("image data is too large")
	}
	return data, mimeType, nil
}

func imageDimensions(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func imageExt(mimeType string) string {
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}
