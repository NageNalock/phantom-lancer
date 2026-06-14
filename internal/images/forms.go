package images

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func ParseMultipartRequest(r *http.Request) (ImagineRequest, error) {
	mode := strings.TrimSpace(r.FormValue("mode"))
	model := strings.TrimSpace(r.FormValue("model"))
	aspectRatio := strings.TrimSpace(r.FormValue("aspect_ratio"))
	resolution := strings.TrimSpace(r.FormValue("resolution"))
	responseFormat := strings.TrimSpace(r.FormValue("response_format"))
	n, err := ParseCount(r.FormValue("n"))
	if err != nil {
		return ImagineRequest{}, err
	}

	imageSlots := 0
	switch mode {
	case "", ModeTextToImage:
		mode = ModeTextToImage
	case ModeImageToImage:
		imageSlots = 1
	case ModeMultiImageEdit:
		imageSlots = 3
	default:
		return ImagineRequest{}, errors.New("mode is invalid")
	}
	inputs, err := parseImageInputs(r, imageSlots)
	if err != nil {
		return ImagineRequest{}, err
	}
	return NormalizeRequest(ImagineRequest{
		Mode:           mode,
		Prompt:         r.FormValue("prompt"),
		Model:          model,
		AspectRatio:    aspectRatio,
		Resolution:     resolution,
		ResponseFormat: responseFormat,
		N:              n,
		Images:         inputs,
	}), nil
}

func parseImageInputs(r *http.Request, maxSlots int) ([]ImageInput, error) {
	images := make([]ImageInput, 0, 3)
	for i := 1; i <= maxSlots; i++ {
		uploadInput, uploadErr := imageFromUpload(r, i)
		if uploadErr != nil {
			return nil, uploadErr
		}
		if uploadInput.URL != "" {
			images = append(images, uploadInput)
			continue
		}
		rawAssetID := strings.TrimSpace(r.FormValue(fmt.Sprintf("source_asset_%d", i)))
		if rawAssetID == "" {
			rawAssetID = strings.TrimSpace(r.FormValue(fmt.Sprintf("image_asset_%d", i)))
		}
		if rawAssetID != "" {
			kind, bareID := splitKindedAssetID(rawAssetID)
			if !assetIDPattern.MatchString(bareID) {
				return nil, fmt.Errorf("image asset %d is invalid", i)
			}
			qualified := rawAssetID
			if kind == "" {
				qualified = "legacy:" + bareID
			}
			images = append(images, ImageInput{
				URL:         "asset:" + qualified,
				SourceType:  "library_asset",
				SourceLabel: bareID,
				URLRedacted: bareID,
			})
			continue
		}
		rawURL := strings.TrimSpace(r.FormValue(fmt.Sprintf("image_url_%d", i)))
		if rawURL == "" {
			continue
		}
		if err := ValidateImageURL(rawURL); err != nil {
			return nil, fmt.Errorf("image url %d: %w", i, err)
		}
		images = append(images, ImageInput{
			URL:         rawURL,
			SourceType:  "url",
			SourceLabel: redactedURL(rawURL),
			URLRedacted: redactedURL(rawURL),
		})
	}
	return images, nil
}

func imageFromUpload(r *http.Request, index int) (ImageInput, error) {
	file, header, err := r.FormFile(fmt.Sprintf("image_file_%d", index))
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return ImageInput{}, nil
		}
		return ImageInput{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxImageBytes+1))
	if err != nil {
		return ImageInput{}, err
	}
	if len(data) > MaxImageBytes {
		return ImageInput{}, fmt.Errorf("image file %d is larger than %d MB", index, MaxImageBytes>>20)
	}
	mimeType := http.DetectContentType(data)
	if !AllowedImageMime(mimeType) {
		return ImageInput{}, fmt.Errorf("image file %d must be jpeg, png, gif, or webp", index)
	}
	sourceLabel := "upload"
	if header != nil && header.Filename != "" {
		sourceLabel = header.Filename
	}
	return ImageInput{
		URL:         "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data),
		SourceType:  "upload",
		SourceLabel: sourceLabel,
		MimeType:    mimeType,
		SizeBytes:   int64(len(data)),
	}, nil
}

func redactedURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if strings.HasPrefix(rawURL, "data:image/") {
		return "data:image/[redacted]"
	}
	if len(rawURL) <= 140 {
		return rawURL
	}
	return rawURL[:100] + "..." + rawURL[len(rawURL)-24:]
}

func splitKindedAssetID(v string) (kind, bareID string) {
	v = strings.TrimSpace(v)
	if idx := strings.Index(v, ":"); idx > 0 {
		prefix := v[:idx]
		if prefix == "legacy" || prefix == "media" {
			return prefix, strings.TrimSpace(v[idx+1:])
		}
	}
	return "", v
}

func ParseMediaMultipartRequest(r *http.Request, mediaType, mode string) (ImagineRequest, error) {
	model := strings.TrimSpace(r.FormValue("model"))
	aspectRatio := strings.TrimSpace(r.FormValue("aspect_ratio"))
	resolution := strings.TrimSpace(r.FormValue("resolution"))
	responseFormat := strings.TrimSpace(r.FormValue("response_format"))
	n, err := ParseCount(r.FormValue("n"))
	if err != nil {
		return ImagineRequest{}, err
	}
	mediaTypeNorm := NormalizeMediaType(mediaType)
	mode = strings.TrimSpace(mode)
	if mode == "" {
		if mediaTypeNorm == MediaTypeVideo {
			mode = VideoModeTextToVideo
		} else {
			mode = ModeTextToImage
		}
	}
	switch mediaTypeNorm {
	case MediaTypeImage:
		switch mode {
		case ModeTextToImage, ModeImageToImage, ModeMultiImageEdit:
		default:
			return ImagineRequest{}, fmt.Errorf("mode %q is not valid for media_type image", mode)
		}
	case MediaTypeVideo:
		switch mode {
		case VideoModeTextToVideo, VideoModeImageToVideo, VideoModeMultiImageVideo, VideoModeKeyframes:
		default:
			return ImagineRequest{}, fmt.Errorf("mode %q is not valid for media_type video", mode)
		}
	}
	imageSlots := 0
	switch mode {
	case ModeImageToImage, VideoModeImageToVideo:
		imageSlots = 1
	case ModeMultiImageEdit, VideoModeMultiImageVideo:
		imageSlots = 3
	case VideoModeKeyframes:
		imageSlots = 6
	}
	inputs, err := parseImageInputs(r, imageSlots)
	if err != nil {
		return ImagineRequest{}, err
	}
	return NormalizeRequest(ImagineRequest{
		Mode:           mode,
		Prompt:         r.FormValue("prompt"),
		Model:          model,
		AspectRatio:    aspectRatio,
		Resolution:     resolution,
		ResponseFormat: responseFormat,
		N:              n,
		Images:         inputs,
	}), nil
}
