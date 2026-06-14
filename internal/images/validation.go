package images

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"phantom-lancer/internal/storage"
)

var (
	allowedAspectRatios = map[string]bool{"": true, "1:1": true, "16:9": true, "9:16": true, "4:3": true, "3:4": true, "3:2": true, "2:3": true}
	allowedResolutions  = map[string]bool{"": true, "1k": true, "2k": true}
	allowedFormats      = map[string]bool{"url": true, "b64_json": true}
	modelNamePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,79}$`)
	assetIDPattern      = regexp.MustCompile(`^(imgasset|medasset)_[A-Za-z0-9_-]{8,80}$`)
)

func NormalizeRequest(request ImagineRequest) ImagineRequest {
	request.Mode = strings.TrimSpace(request.Mode)
	if request.Mode == "" {
		request.Mode = ModeTextToImage
	}
	request.Prompt = strings.TrimSpace(request.Prompt)
	request.Model = strings.TrimSpace(request.Model)
	if request.Model == "" {
		request.Model = "grok-imagine-image-quality"
	}
	request.AspectRatio = strings.TrimSpace(request.AspectRatio)
	request.Resolution = strings.TrimSpace(request.Resolution)
	request.ResponseFormat = strings.TrimSpace(request.ResponseFormat)
	if request.ResponseFormat == "" {
		request.ResponseFormat = "url"
	}
	if request.N == 0 {
		request.N = 1
	}
	return request
}

func ValidateRequest(request ImagineRequest) error {
	request = NormalizeRequest(request)
	if request.Prompt == "" {
		return errors.New("prompt is required")
	}
	if len(request.Prompt) > 8000 {
		return errors.New("prompt is too long")
	}
	if !modelNamePattern.MatchString(request.Model) {
		return errors.New("model name is invalid")
	}
	if !allowedAspectRatios[request.AspectRatio] {
		return errors.New("aspect ratio is not supported")
	}
	if !allowedResolutions[request.Resolution] {
		return errors.New("resolution is not supported")
	}
	if !allowedFormats[request.ResponseFormat] {
		return errors.New("response format is not supported")
	}
	if request.N < 1 || request.N > 10 {
		return errors.New("image count must be between 1 and 10")
	}
	switch request.Mode {
	case ModeTextToImage:
		if len(request.Images) != 0 {
			return errors.New("text-to-image does not accept source images")
		}
	case ModeImageToImage:
		if len(request.Images) != 1 {
			return errors.New("image-to-image requires exactly one source image")
		}
	case ModeMultiImageEdit:
		if len(request.Images) < 2 || len(request.Images) > 3 {
			return errors.New("multi-image editing requires two or three source images")
		}
	default:
		return errors.New("mode is invalid")
	}
	return nil
}

func ParseCount(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 1, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("image count is invalid")
	}
	if n < 1 || n > 10 {
		return 0, errors.New("image count must be between 1 and 10")
	}
	return n, nil
}

func ValidateImageURL(rawURL string) error {
	if len(rawURL) > 4096 {
		return errors.New("url is too long")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("url is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" && !strings.HasPrefix(rawURL, "data:image/") {
		return errors.New("url must be http, https, or a data:image URI")
	}
	if (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host == "" {
		return errors.New("url host is required")
	}
	return nil
}

func AllowedImageMime(mimeType string) bool {
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func SettingSupported(field, value string) error {
	switch field {
	case "response_format":
		if !allowedFormats[value] {
			return fmt.Errorf("response format is not supported")
		}
	case "resolution":
		if !allowedResolutions[value] {
			return fmt.Errorf("resolution is not supported")
		}
	case "aspect_ratio":
		if !allowedAspectRatios[value] {
			return fmt.Errorf("aspect ratio is not supported")
		}
	}
	return nil
}

func ValidatePrompt(prompt storage.ImagePrompt) error {
	prompt = storage.NormalizeImagePrompt(prompt)
	if prompt.Title == "" {
		return errors.New("prompt title is required")
	}
	if prompt.Prompt == "" {
		return errors.New("prompt is required")
	}
	if len(prompt.Prompt) > 8000 {
		return errors.New("prompt is too long")
	}
	switch prompt.Mode {
	case ModeTextToImage, ModeImageToImage, ModeMultiImageEdit:
	case VideoModeTextToVideo, VideoModeImageToVideo, VideoModeMultiImageVideo, VideoModeKeyframes:
	default:
		return errors.New("prompt mode is invalid")
	}
	if prompt.Model != "" && !modelNamePattern.MatchString(prompt.Model) {
		return errors.New("model name is invalid")
	}
	switch prompt.Mode {
	case VideoModeTextToVideo, VideoModeImageToVideo, VideoModeMultiImageVideo, VideoModeKeyframes:
		if prompt.AspectRatio != "" && !allowedAspectRatios[prompt.AspectRatio] {
			return errors.New("aspect ratio is not supported")
		}
		if prompt.Resolution != "" && !allowedResolutions[prompt.Resolution] {
			return errors.New("resolution is not supported")
		}
	default:
		if !allowedAspectRatios[prompt.AspectRatio] {
			return errors.New("aspect ratio is not supported")
		}
		if !allowedResolutions[prompt.Resolution] {
			return errors.New("resolution is not supported")
		}
	}
	if prompt.ImageCount < 1 || prompt.ImageCount > 10 {
		return errors.New("image count must be between 1 and 10")
	}
	if len(prompt.Tags) > 12 {
		return errors.New("prompt tags are too many")
	}
	return nil
}
