package images

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type XAIClient struct {
	baseURL string
	http    *http.Client
}

type xaiImageResponse struct {
	Data []struct {
		URL           string `json:"url"`
		B64JSON       string `json:"b64_json"`
		MimeType      string `json:"mime_type"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
	Usage map[string]any `json:"usage"`
}

func NewXAIClient(baseURL string, httpClient *http.Client) *XAIClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 150 * time.Second}
	}
	return &XAIClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    httpClient,
	}
}

func (c *XAIClient) Imagine(ctx context.Context, apiKey string, request ImagineRequest) (*ImagineResult, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, ErrAPIKeyMissing
	}
	request = NormalizeRequest(request)
	if err := ValidateRequest(request); err != nil {
		return nil, err
	}
	endpoint, payload, err := RequestPayload(request)
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
		return nil, fmt.Errorf("xAI request failed: %s", redactSecret(message, apiKey))
	}

	var xaiResp xaiImageResponse
	if err := json.Unmarshal(responseBody, &xaiResp); err != nil {
		return nil, err
	}
	result := &ImagineResult{
		Mode:      request.Mode,
		ModeLabel: ModeLabel(request.Mode),
		Model:     request.Model,
		Endpoint:  endpoint,
		Usage:     xaiResp.Usage,
	}
	if result.Usage == nil {
		result.Usage = map[string]any{}
	}
	for _, item := range xaiResp.Data {
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

func RequestPayload(request ImagineRequest) (string, map[string]any, error) {
	request = NormalizeRequest(request)
	payload := map[string]any{
		"model":  request.Model,
		"prompt": request.Prompt,
	}
	if request.AspectRatio != "" {
		payload["aspect_ratio"] = request.AspectRatio
	}
	if request.Resolution != "" {
		payload["resolution"] = request.Resolution
	}
	if request.ResponseFormat != "" {
		payload["response_format"] = request.ResponseFormat
	}
	if request.N > 0 {
		payload["n"] = request.N
	}

	switch request.Mode {
	case ModeTextToImage:
		return "/images/generations", payload, nil
	case ModeImageToImage:
		if len(request.Images) != 1 {
			return "", nil, errors.New("image-to-image requires exactly one source image")
		}
		payload["image"] = imagePayload(request.Images[0])
		return "/images/edits", payload, nil
	case ModeMultiImageEdit:
		if len(request.Images) < 2 || len(request.Images) > 3 {
			return "", nil, errors.New("multi-image editing requires two or three source images")
		}
		images := make([]map[string]string, 0, len(request.Images))
		for _, image := range request.Images {
			images = append(images, imagePayload(image))
		}
		payload["images"] = images
		return "/images/edits", payload, nil
	default:
		return "", nil, errors.New("mode is invalid")
	}
}

func imagePayload(image ImageInput) map[string]string {
	return map[string]string{
		"type": "image_url",
		"url":  image.URL,
	}
}

func redactSecret(message, secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return message
	}
	return strings.ReplaceAll(message, secret, "[redacted]")
}
