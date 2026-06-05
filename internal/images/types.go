package images

import "errors"

const (
	ModeTextToImage    = "text_to_image"
	ModeImageToImage   = "image_to_image"
	ModeMultiImageEdit = "multi_image_edit"

	MaxFormBytes     = 40 << 20
	MaxImageBytes    = 12 << 20
	MaxSettingsBytes = 64 << 10
)

var ErrAPIKeyMissing = errors.New("xAI API key is not configured")

type ImageInput struct {
	URL         string
	SourceType  string
	SourceLabel string
	MimeType    string
	SizeBytes   int64
	URLRedacted string
}

type ImagineRequest struct {
	Mode           string
	Prompt         string
	Model          string
	AspectRatio    string
	Resolution     string
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

func ModeLabel(mode string) string {
	switch mode {
	case ModeTextToImage:
		return "文生图"
	case ModeImageToImage:
		return "图生图"
	case ModeMultiImageEdit:
		return "多图编辑"
	default:
		return mode
	}
}
