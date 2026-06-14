package images

import (
	"errors"
	"strings"
)

type MediaType string

const (
	MediaTypeImage MediaType = "image"
	MediaTypeVideo MediaType = "video"
)

type ProviderID string

const (
	ProviderXAI   ProviderID = "xai"
	ProviderAgnes ProviderID = "agnes"
)

type ModelParameterSchema struct {
	SizePresets       []string `json:"sizePresets,omitempty"`
	DefaultSize       string   `json:"defaultSize,omitempty"`
	DefaultWidth      int      `json:"defaultWidth,omitempty"`
	DefaultHeight     int      `json:"defaultHeight,omitempty"`
	DurationPresets   []string `json:"durationPresets,omitempty"`
	DefaultDuration   string   `json:"defaultDuration,omitempty"`
	DefaultNumFrames  int      `json:"defaultNumFrames,omitempty"`
	DefaultFrameRate  int      `json:"defaultFrameRate,omitempty"`
	MaxNumFrames      int      `json:"maxNumFrames,omitempty"`
	NumFramesStep     int      `json:"numFramesStep,omitempty"`
	MinFrameRate      int      `json:"minFrameRate,omitempty"`
	MaxFrameRate      int      `json:"maxFrameRate,omitempty"`
	DefaultN          int      `json:"defaultN,omitempty"`
	MaxN              int      `json:"maxN,omitempty"`
	ResponseFormats   []string `json:"responseFormats,omitempty"`
	DefaultFormat     string   `json:"defaultFormat,omitempty"`
}

type ModelCapability struct {
	Provider       ProviderID          `json:"provider"`
	Model          string              `json:"model"`
	Label          string              `json:"label"`
	MediaType      MediaType           `json:"mediaType"`
	Deprecated     bool                `json:"deprecated"`
	DefaultFor     []string            `json:"defaultFor,omitempty"`
	SupportedModes []string            `json:"supportedModes"`
	Parameters     ModelParameterSchema `json:"parameters"`
	MinReferences  int                 `json:"minReferences"`
	MaxReferences  int                 `json:"maxReferences"`
}

var modelCatalog = []ModelCapability{
	{
		Provider:       ProviderXAI,
		Model:          "grok-imagine-image-quality",
		Label:          "Grok Imagine Quality",
		MediaType:      MediaTypeImage,
		Deprecated:     false,
		DefaultFor:     []string{"xai:image"},
		SupportedModes: []string{ModeTextToImage, ModeImageToImage, ModeMultiImageEdit},
		Parameters: ModelParameterSchema{
			DefaultN:        1,
			MaxN:            10,
			ResponseFormats: []string{"url", "b64_json"},
			DefaultFormat:   "url",
		},
		MinReferences: 0,
		MaxReferences: 3,
	},
	{
		Provider:       ProviderXAI,
		Model:          "grok-imagine-image",
		Label:          "Grok Imagine Fast",
		MediaType:      MediaTypeImage,
		Deprecated:     false,
		SupportedModes: []string{ModeTextToImage, ModeImageToImage, ModeMultiImageEdit},
		Parameters: ModelParameterSchema{
			DefaultN:        1,
			MaxN:            10,
			ResponseFormats: []string{"url", "b64_json"},
			DefaultFormat:   "url",
		},
		MinReferences: 0,
		MaxReferences: 3,
	},
	{
		Provider:       ProviderAgnes,
		Model:          "agnes-image-2.1-flash",
		Label:          "Agnes Image 2.1 Flash",
		MediaType:      MediaTypeImage,
		Deprecated:     false,
		DefaultFor:     []string{"agnes:image"},
		SupportedModes: []string{ModeTextToImage, ModeImageToImage},
		Parameters: ModelParameterSchema{
			SizePresets:     []string{"1024x1024", "1024x768", "768x1024", "1280x720", "720x1280", "1536x1024", "1024x1536"},
			DefaultSize:     "1024x768",
			DefaultN:        1,
			MaxN:            1,
			ResponseFormats: []string{"url", "b64_json"},
			DefaultFormat:   "url",
		},
		MinReferences: 0,
		MaxReferences: 1,
	},
	{
		Provider:       ProviderAgnes,
		Model:          "agnes-image-2.0-flash",
		Label:          "Agnes Image 2.0 Flash",
		MediaType:      MediaTypeImage,
		Deprecated:     false,
		SupportedModes: []string{ModeTextToImage, ModeImageToImage, ModeMultiImageEdit},
		Parameters: ModelParameterSchema{
			SizePresets:     []string{"1024x1024", "1024x768", "768x1024", "1280x720", "720x1280"},
			DefaultSize:     "1024x768",
			DefaultN:        1,
			MaxN:            1,
			ResponseFormats: []string{"url", "b64_json"},
			DefaultFormat:   "url",
		},
		MinReferences: 0,
		MaxReferences: 3,
	},
	{
		Provider:       ProviderAgnes,
		Model:          "agnes-image-1.2",
		Label:          "Agnes Image 1.2",
		MediaType:      MediaTypeImage,
		Deprecated:     true,
		SupportedModes: []string{ModeTextToImage, ModeImageToImage},
		Parameters: ModelParameterSchema{
			SizePresets:     []string{"1024x1024", "1024x768", "768x1024"},
			DefaultSize:     "1024x768",
			DefaultN:        1,
			MaxN:            1,
			ResponseFormats: []string{"url", "b64_json"},
			DefaultFormat:   "url",
		},
		MinReferences: 0,
		MaxReferences: 1,
	},
	{
		Provider:   ProviderAgnes,
		Model:      "agnes-video-v2.0",
		Label:      "Agnes Video V2.0",
		MediaType:  MediaTypeVideo,
		Deprecated: false,
		DefaultFor: []string{"agnes:video"},
		SupportedModes: []string{
			VideoModeTextToVideo,
			VideoModeImageToVideo,
			VideoModeMultiImageVideo,
			VideoModeKeyframes,
		},
		Parameters: ModelParameterSchema{
			SizePresets:      []string{"1152x768", "768x1152", "1024x576", "576x1024", "1280x720", "720x1280"},
			DefaultSize:      "1152x768",
			DefaultWidth:     1152,
			DefaultHeight:    768,
			DurationPresets:  []string{"3s", "5s", "10s", "18s"},
			DefaultDuration:  "5s",
			DefaultNumFrames: 121,
			DefaultFrameRate: 24,
			MaxNumFrames:     441,
			NumFramesStep:    8,
			MinFrameRate:     1,
			MaxFrameRate:     60,
		},
		MinReferences: 0,
		MaxReferences: 3,
	},
	{
		Provider:   ProviderAgnes,
		Model:      "agnes-video-v1.2",
		Label:      "Agnes Video V1.2",
		MediaType:  MediaTypeVideo,
		Deprecated: true,
		SupportedModes: []string{
			VideoModeTextToVideo,
			VideoModeImageToVideo,
		},
		Parameters: ModelParameterSchema{
			SizePresets:      []string{"1152x768", "768x1152"},
			DefaultSize:      "1152x768",
			DefaultWidth:     1152,
			DefaultHeight:    768,
			DurationPresets:  []string{"3s", "5s"},
			DefaultDuration:  "5s",
			DefaultNumFrames: 121,
			DefaultFrameRate: 24,
			MaxNumFrames:     441,
			NumFramesStep:    8,
			MinFrameRate:     1,
			MaxFrameRate:     60,
		},
		MinReferences: 0,
		MaxReferences: 1,
	},
}

func ListModelCapabilities(includeDeprecated bool) []ModelCapability {
	if includeDeprecated {
		out := make([]ModelCapability, len(modelCatalog))
		copy(out, modelCatalog)
		return out
	}
	out := make([]ModelCapability, 0, len(modelCatalog))
	for _, m := range modelCatalog {
		if !m.Deprecated {
			out = append(out, m)
		}
	}
	return out
}

func GetModelCapability(provider ProviderID, model string) (ModelCapability, bool) {
	for _, m := range modelCatalog {
		if m.Provider == provider && m.Model == model {
			return m, true
		}
	}
	return ModelCapability{}, false
}

func ModelsForProvider(provider ProviderID, mediaType MediaType, includeDeprecated bool) []ModelCapability {
	out := make([]ModelCapability, 0)
	for _, m := range modelCatalog {
		if m.Provider != provider {
			continue
		}
		if mediaType != "" && m.MediaType != mediaType {
			continue
		}
		if !includeDeprecated && m.Deprecated {
			continue
		}
		out = append(out, m)
	}
	return out
}

func DefaultModel(provider ProviderID, mediaType MediaType) string {
	needle := string(provider) + ":" + string(mediaType)
	for _, m := range modelCatalog {
		if m.Deprecated {
			continue
		}
		if m.Provider != provider || m.MediaType != mediaType {
			continue
		}
		for _, tag := range m.DefaultFor {
			if tag == needle {
				return m.Model
			}
		}
	}
	models := ModelsForProvider(provider, mediaType, false)
	if len(models) > 0 {
		return models[0].Model
	}
	return ""
}

var (
	ErrModelNotFound       = errors.New("model is not in provider catalog")
	ErrModelDeprecated     = errors.New("model is deprecated")
	ErrMediaTypeMismatch   = errors.New("model does not support this media type")
	ErrModeNotSupported    = errors.New("model does not support this mode")
	ErrReferenceCount      = errors.New("reference image count does not match model capability")
	ErrProviderUnavailable = errors.New("provider is not configured or unavailable")
)

const (
	VideoModeTextToVideo     = "text_to_video"
	VideoModeImageToVideo    = "image_to_video"
	VideoModeMultiImageVideo = "multi_image_video"
	VideoModeKeyframes       = "keyframes"
)

func VideoModeLabel(mode string) string {
	switch mode {
	case VideoModeTextToVideo:
		return "文生视频"
	case VideoModeImageToVideo:
		return "图生视频"
	case VideoModeMultiImageVideo:
		return "多图视频"
	case VideoModeKeyframes:
		return "关键帧动画"
	default:
		return mode
	}
}

func NormalizeProvider(raw string) ProviderID {
	return ProviderID(strings.ToLower(strings.TrimSpace(raw)))
}

func NormalizeMediaType(raw string) MediaType {
	return MediaType(strings.ToLower(strings.TrimSpace(raw)))
}

func ValidateProvider(p ProviderID) error {
	switch p {
	case ProviderXAI, ProviderAgnes:
		return nil
	default:
		return errors.New("provider is unsupported")
	}
}

func ValidateMediaType(m MediaType) error {
	switch m {
	case MediaTypeImage, MediaTypeVideo:
		return nil
	default:
		return errors.New("media type is unsupported")
	}
}

func ValidateNumFrames(numFrames int, max int) error {
	if numFrames <= 0 {
		return errors.New("num_frames must be positive")
	}
	if max > 0 && numFrames > max {
		return errors.New("num_frames exceeds maximum")
	}
	if (numFrames-1)%8 != 0 {
		return errors.New("num_frames must satisfy 8n + 1")
	}
	return nil
}

func ValidateFrameRate(frameRate int, min, max int) error {
	if frameRate < min || frameRate > max {
		return errors.New("frame_rate is out of supported range")
	}
	return nil
}

func ParseSize(raw string) (width, height int, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, errors.New("size is required")
	}
	parts := strings.SplitN(raw, "x", 2)
	if len(parts) != 2 {
		return 0, 0, errors.New("size must be in WIDTHxHEIGHT format")
	}
	w, errW := atoiPositive(parts[0])
	h, errH := atoiPositive(parts[1])
	if errW != nil || errH != nil {
		return 0, 0, errors.New("size dimensions must be positive integers")
	}
	return w, h, nil
}

func atoiPositive(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(r-'0')
	}
	if n <= 0 {
		return 0, errors.New("not positive")
	}
	return n, nil
}
