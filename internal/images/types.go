package images

import (
	"errors"

	"phantom-lancer/internal/storage"
)

const (
	ModeTextToImage    = "text_to_image"
	ModeImageToImage   = "image_to_image"
	ModeMultiImageEdit = "multi_image_edit"

	MaxFormBytes         = 40 << 20
	MaxImageBytes        = 12 << 20
	MaxSettingsBytes     = 64 << 10
	MaxVideoDownloadBytes = 512 << 20
)

var (
	ErrAPIKeyMissing       = errors.New("provider API key is not configured")
	ErrXAIAPIKeyMissing    = errors.New("xAI API key is not configured")
	ErrAgnesAPIKeyMissing  = errors.New("Agnes API key is not configured")
)

type ImageInput struct {
	URL         string
	SourceType  string
	SourceLabel string
	MimeType    string
	SizeBytes   int64
	URLRedacted string
}

type ImagineRequest struct {
	Provider       ProviderID
	Mode           string
	Prompt         string
	Model          string
	AspectRatio    string
	Resolution     string
	Size           string
	Width          int
	Height         int
	ResponseFormat string
	N              int
	Images         []ImageInput
}

type ResultImage struct {
	URL           string
	DataURL       string
	MimeType      string
	RevisedPrompt string
}

type ImagineResult struct {
	Mode      string
	ModeLabel string
	Model     string
	Endpoint  string
	Images    []ResultImage
	Usage     map[string]any
}

type VideoParameters struct {
	NumFrames int `json:"numFrames,omitempty"`
	FrameRate int `json:"frameRate,omitempty"`
	Seed      int `json:"seed,omitempty"`
	Width     int `json:"width,omitempty"`
	Height    int `json:"height,omitempty"`
}

type VideoRequest struct {
	Provider   ProviderID
	Mode       string
	Prompt     string
	Model      string
	Parameters VideoParameters
	Images     []ImageInput
}

type VideoCreateResult struct {
	ProviderTaskID  string
	ProviderVideoID string
	Status          string
	Progress        int
}

type VideoPollResult struct {
	ProviderTaskID  string
	ProviderVideoID string
	Status          string
	Progress        int
	VideoURL        string
	Width           int
	Height          int
	NumFrames       int
	FrameRate       int
	Seconds         float64
	SizeBytes       int64
	ErrorMessage    string
	RawStatus       string
}

type Status struct {
	Available       bool   `json:"available"`
	Provider        string `json:"provider"`
	HasAPIKey       bool   `json:"hasApiKey"`
	MaskedAPIKey    string `json:"maskedApiKey"`
	DefaultModel    string `json:"defaultModel"`
	HistoryCount    int    `json:"historyCount"`
	LastJobStatus   string `json:"lastJobStatus,omitempty"`
	LastJobID       string `json:"lastJobId,omitempty"`
	LastError       string `json:"lastError,omitempty"`
	LastCompletedAt string `json:"lastCompletedAt,omitempty"`
}

type ProviderStatus struct {
	Provider            ProviderID `json:"provider"`
	Enabled             bool       `json:"enabled"`
	HasAPIKey           bool       `json:"hasApiKey"`
	MaskedAPIKey        string     `json:"maskedApiKey,omitempty"`
	DefaultImageModel   string     `json:"defaultImageModel,omitempty"`
	DefaultVideoModel   string     `json:"defaultVideoModel,omitempty"`
	LastTestedAt        string     `json:"lastTestedAt,omitempty"`
	LastError           string     `json:"lastError,omitempty"`
	ImageJobCount       int        `json:"imageJobCount,omitempty"`
	VideoJobCount       int        `json:"videoJobCount,omitempty"`
}

type ProvidersStatus struct {
	Providers   []ProviderStatus    `json:"providers"`
	Models      []ModelCapability   `json:"models"`
	DefaultXAI  string              `json:"defaultXaiProvider"`
}

type LibraryUploadResult struct {
	Asset     storage.ImageAsset `json:"asset"`
	Duplicate bool               `json:"duplicate"`
}

func ModeLabel(mode string) string {
	switch mode {
	case ModeTextToImage:
		return "文生图"
	case ModeImageToImage:
		return "图生图"
	case ModeMultiImageEdit:
		return "多图编辑"
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
