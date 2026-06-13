package images

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	agnesBaseURL              = "https://apihub.agnes-ai.com"
	agnesImagesEndpoint       = "/v1/images/generations"
	agnesVideosEndpoint       = "/v1/videos"
	agnesVideoPollEndpoint    = "/v1/videos/%s"
	agnesVideoPollAltEndpoint = "/agnesapi?video_id=%s"
)

type AgnesClient struct {
	baseURL string
	http    *http.Client
}

func NewAgnesClient(baseURL string, httpClient *http.Client) *AgnesClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 180 * time.Second}
	}
	return &AgnesClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    httpClient,
	}
}

type agnesImageResponse struct {
	Created int64 `json:"created"`
	Data    []struct {
		URL           string `json:"url"`
		B64JSON       string `json:"b64_json"`
		MimeType      string `json:"mime_type"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
	Usage map[string]any `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

func (c *AgnesClient) GenerateImage(ctx context.Context, apiKey string, request ImagineRequest) (*ImagineResult, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, ErrAgnesAPIKeyMissing
	}
	endpoint, payload, err := agnesImagePayload(request)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(responseBody))
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("Agnes image request failed: %s", redactSecret(message, apiKey))
	}
	var agnesResp agnesImageResponse
	if err := json.Unmarshal(responseBody, &agnesResp); err != nil {
		return nil, err
	}
	if agnesResp.Error != nil && agnesResp.Error.Message != "" {
		return nil, fmt.Errorf("Agnes image request failed: %s", redactSecret(agnesResp.Error.Message, apiKey))
	}
	result := &ImagineResult{
		Mode:      request.Mode,
		ModeLabel: ModeLabel(request.Mode),
		Model:     request.Model,
		Endpoint:  endpoint,
		Usage:     agnesResp.Usage,
	}
	if result.Usage == nil {
		result.Usage = map[string]any{}
	}
	for _, item := range agnesResp.Data {
		mimeType := item.MimeType
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
		image := ResultImage{
			URL:           item.URL,
			MimeType:      mimeType,
			RevisedPrompt: item.RevisedPrompt,
		}
		if item.B64JSON != "" {
			image.DataURL = "data:" + mimeType + ";base64," + item.B64JSON
		}
		result.Images = append(result.Images, image)
	}
	return result, nil
}

func agnesImagePayload(request ImagineRequest) (string, map[string]any, error) {
	cap, ok := GetModelCapability(ProviderAgnes, request.Model)
	if !ok {
		return "", nil, ErrModelNotFound
	}
	if cap.MediaType != MediaTypeImage {
		return "", nil, ErrMediaTypeMismatch
	}
	if cap.Deprecated {
		return "", nil, ErrModelDeprecated
	}
	refCount := len(request.Images)
	if refCount < cap.MinReferences || refCount > cap.MaxReferences {
		return "", nil, ErrReferenceCount
	}
	switch request.Mode {
	case ModeTextToImage:
		if refCount != 0 {
			return "", nil, ErrReferenceCount
		}
	case ModeImageToImage:
		if refCount != 1 {
			return "", nil, ErrReferenceCount
		}
	case ModeMultiImageEdit:
		if refCount < 2 || refCount > cap.MaxReferences {
			return "", nil, ErrReferenceCount
		}
	}
	modeSupported := false
	for _, m := range cap.SupportedModes {
		if m == request.Mode {
			modeSupported = true
			break
		}
	}
	if !modeSupported {
		return "", nil, ErrModeNotSupported
	}

	size := strings.TrimSpace(request.Size)
	if size == "" && request.Width > 0 && request.Height > 0 {
		size = fmt.Sprintf("%dx%d", request.Width, request.Height)
	}
	if size == "" {
		size = cap.Parameters.DefaultSize
	}
	if _, _, err := ParseSize(size); err != nil {
		return "", nil, err
	}

	payload := map[string]any{
		"model":  request.Model,
		"prompt": request.Prompt,
		"size":   size,
	}

	extraBody := map[string]any{}
	format := strings.TrimSpace(request.ResponseFormat)
	if format == "" {
		format = cap.Parameters.DefaultFormat
	}
	if format == "url" || format == "b64_json" {
		extraBody["response_format"] = format
	}

	if refCount == 1 {
		extraBody["image"] = []string{request.Images[0].URL}
	} else if refCount > 1 {
		urls := make([]string, 0, refCount)
		for _, img := range request.Images {
			urls = append(urls, img.URL)
		}
		extraBody["image"] = urls
	}

	if format == "b64_json" && refCount == 0 {
		payload["return_base64"] = true
	}

	if len(extraBody) > 0 {
		payload["extra_body"] = extraBody
	}

	return agnesImagesEndpoint, payload, nil
}

type agnesVideoCreateResponse struct {
	TaskID  string `json:"task_id"`
	VideoID string `json:"video_id"`
	Status  string `json:"status"`
	Error   *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

func (c *AgnesClient) CreateVideo(ctx context.Context, apiKey string, request VideoRequest) (*VideoCreateResult, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, ErrAgnesAPIKeyMissing
	}
	endpoint, payload, err := agnesVideoPayload(request)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(responseBody))
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("Agnes video create failed: %s", redactSecret(message, apiKey))
	}
	var createResp agnesVideoCreateResponse
	if err := json.Unmarshal(responseBody, &createResp); err != nil {
		return nil, err
	}
	if createResp.Error != nil && createResp.Error.Message != "" {
		return nil, fmt.Errorf("Agnes video create failed: %s", redactSecret(createResp.Error.Message, apiKey))
	}
	if createResp.VideoID == "" && createResp.TaskID == "" {
		return nil, errors.New("Agnes video response missing task_id and video_id")
	}
	return &VideoCreateResult{
		ProviderTaskID:  createResp.TaskID,
		ProviderVideoID: createResp.VideoID,
		Status:          normalizeAgnesVideoStatus(createResp.Status),
	}, nil
}

type agnesVideoPollResponse struct {
	TaskID               string `json:"task_id"`
	VideoID              string `json:"video_id"`
	ID                   string `json:"id"`
	Status               string `json:"status"`
	Progress             any    `json:"progress"`
	RemixedFromVideoID   string `json:"remixed_from_video_id"`
	DownloadURL          string `json:"download_url"`
	URL                  string `json:"url"`
	VideoURL             string `json:"video_url"`
	Width                int    `json:"width"`
	Height               int    `json:"height"`
	NumFrames            int    `json:"num_frames"`
	FrameRate            int    `json:"frame_rate"`
	Seconds              any    `json:"seconds"`
	SizeBytes            int64  `json:"size_bytes"`
	ErrorMessage         string `json:"error_message"`
	Error                *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

func (c *AgnesClient) GetVideo(ctx context.Context, apiKey string, taskID, videoID string) (*VideoPollResult, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, ErrAgnesAPIKeyMissing
	}

	endpoint := ""
	if videoID != "" {
		endpoint = fmt.Sprintf(agnesVideoPollAltEndpoint, videoID)
	} else if taskID != "" {
		endpoint = fmt.Sprintf(agnesVideoPollEndpoint, taskID)
	} else {
		return nil, errors.New("provider video id and task id are both empty")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		if videoID != "" && taskID != "" {
			return c.getVideoByTaskID(ctx, apiKey, taskID)
		}
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if videoID != "" && taskID != "" && resp.StatusCode >= 400 {
			return c.getVideoByTaskID(ctx, apiKey, taskID)
		}
		message := strings.TrimSpace(string(responseBody))
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("Agnes video poll failed: %s", redactSecret(message, apiKey))
	}

	var pollResp agnesVideoPollResponse
	if err := json.Unmarshal(responseBody, &pollResp); err != nil {
		return nil, err
	}
	return parseAgnesVideoPoll(pollResp, apiKey, taskID, videoID), nil
}

func (c *AgnesClient) getVideoByTaskID(ctx context.Context, apiKey, taskID string) (*VideoPollResult, error) {
	endpoint := fmt.Sprintf(agnesVideoPollEndpoint, taskID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(responseBody))
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("Agnes video poll failed: %s", redactSecret(message, apiKey))
	}
	var pollResp agnesVideoPollResponse
	if err := json.Unmarshal(responseBody, &pollResp); err != nil {
		return nil, err
	}
	return parseAgnesVideoPoll(pollResp, apiKey, taskID, ""), nil
}

func parseAgnesVideoPoll(p agnesVideoPollResponse, apiKey, taskID, videoID string) *VideoPollResult {
	result := &VideoPollResult{
		ProviderTaskID:  firstNonEmpty(p.TaskID, taskID),
		ProviderVideoID: firstNonEmpty(p.VideoID, p.ID, videoID),
		RawStatus:       p.Status,
		Status:          normalizeAgnesVideoStatus(p.Status),
		Width:           p.Width,
		Height:          p.Height,
		NumFrames:       p.NumFrames,
		FrameRate:       p.FrameRate,
		SizeBytes:       p.SizeBytes,
	}
	result.Progress = parseIntProgress(p.Progress)
	result.VideoURL = firstNonEmpty(p.RemixedFromVideoID, p.VideoURL, p.URL, p.DownloadURL)
	if p.Error != nil && p.Error.Message != "" {
		result.ErrorMessage = redactSecret(p.Error.Message, apiKey)
	} else if p.ErrorMessage != "" {
		result.ErrorMessage = redactSecret(p.ErrorMessage, apiKey)
	}
	if result.FrameRate > 0 && result.NumFrames > 0 {
		result.Seconds = float64(result.NumFrames) / float64(result.FrameRate)
	} else if s, ok := parseFloatSeconds(p.Seconds); ok {
		result.Seconds = s
	}
	return result
}

func parseIntProgress(v any) int {
	switch val := v.(type) {
	case float64:
		n := int(val)
		if n < 0 {
			return 0
		}
		if n > 100 {
			return 100
		}
		return n
	case int:
		if val < 0 {
			return 0
		}
		if val > 100 {
			return 100
		}
		return val
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			if n < 0 {
				return 0
			}
			if n > 100 {
				return 100
			}
			return n
		}
	}
	return 0
}

func parseFloatSeconds(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

func normalizeAgnesVideoStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "queued", "pending", "waiting":
		return "queued"
	case "in_progress", "processing", "running", "generating":
		return "running"
	case "completed", "succeeded", "success", "done":
		return "completed"
	case "failed", "error", "cancelled", "canceled", "rejected":
		return "failed"
	case "interrupted", "timeout", "timed_out":
		return "interrupted"
	default:
		return "unknown"
	}
}

func agnesVideoPayload(request VideoRequest) (string, map[string]any, error) {
	cap, ok := GetModelCapability(ProviderAgnes, request.Model)
	if !ok {
		return "", nil, ErrModelNotFound
	}
	if cap.MediaType != MediaTypeVideo {
		return "", nil, ErrMediaTypeMismatch
	}
	if cap.Deprecated {
		return "", nil, ErrModelDeprecated
	}
	modeSupported := false
	for _, m := range cap.SupportedModes {
		if m == request.Mode {
			modeSupported = true
			break
		}
	}
	if !modeSupported {
		return "", nil, ErrModeNotSupported
	}
	refCount := len(request.Images)
	switch request.Mode {
	case VideoModeTextToVideo:
		if refCount != 0 {
			return "", nil, ErrReferenceCount
		}
	case VideoModeImageToVideo:
		if refCount != 1 {
			return "", nil, ErrReferenceCount
		}
	case VideoModeMultiImageVideo, VideoModeKeyframes:
		if refCount < 2 || refCount > cap.MaxReferences {
			return "", nil, ErrReferenceCount
		}
	}
	if refCount < cap.MinReferences || refCount > cap.MaxReferences {
		return "", nil, ErrReferenceCount
	}

	width := request.Parameters.Width
	height := request.Parameters.Height
	if width <= 0 || height <= 0 {
		width = cap.Parameters.DefaultWidth
		height = cap.Parameters.DefaultHeight
	}
	if width <= 0 || height <= 0 {
		return "", nil, errors.New("video width and height are required")
	}

	numFrames := request.Parameters.NumFrames
	if numFrames <= 0 {
		numFrames = cap.Parameters.DefaultNumFrames
	}
	if err := ValidateNumFrames(numFrames, cap.Parameters.MaxNumFrames); err != nil {
		return "", nil, err
	}
	frameRate := request.Parameters.FrameRate
	if frameRate <= 0 {
		frameRate = cap.Parameters.DefaultFrameRate
	}
	if err := ValidateFrameRate(frameRate, cap.Parameters.MinFrameRate, cap.Parameters.MaxFrameRate); err != nil {
		return "", nil, err
	}

	payload := map[string]any{
		"model":      request.Model,
		"prompt":     request.Prompt,
		"width":      width,
		"height":     height,
		"num_frames": numFrames,
		"frame_rate": frameRate,
	}
	if request.Parameters.Seed > 0 {
		payload["seed"] = request.Parameters.Seed
	}

	extraBody := map[string]any{}

	switch request.Mode {
	case VideoModeTextToVideo:
	case VideoModeImageToVideo:
		if refCount == 1 && request.Images[0].URL != "" {
			payload["image"] = request.Images[0].URL
		}
	case VideoModeMultiImageVideo:
		if refCount >= 2 {
			urls := make([]string, 0, refCount)
			for _, img := range request.Images {
				if img.URL != "" {
					urls = append(urls, img.URL)
				}
			}
			extraBody["image"] = urls
		}
	case VideoModeKeyframes:
		if refCount >= 2 {
			urls := make([]string, 0, refCount)
			for _, img := range request.Images {
				if img.URL != "" {
					urls = append(urls, img.URL)
				}
			}
			extraBody["image"] = urls
			extraBody["mode"] = "keyframes"
		}
	default:
		return "", nil, ErrModeNotSupported
	}

	if len(extraBody) > 0 {
		payload["extra_body"] = extraBody
	}

	return agnesVideosEndpoint, payload, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
