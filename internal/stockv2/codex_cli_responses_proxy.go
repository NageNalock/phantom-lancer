package stockv2

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	codexResponsesProxyRequestMaxBytes = 16 << 20
	codexResponsesProxyEventMaxBytes   = 4 << 20
	codexResponsesToolNameMaxBytes     = 64
	codexResponsesProxySlowRequest     = 2 * time.Minute
)

type codexNamespaceToolName struct {
	Namespace string
	Name      string
}

func (s *Service) agentCodexCLIProxyBaseURL(providerID string) (string, error) {
	s.agentMCPMu.RLock()
	mcpURL := s.agentMCPURL
	s.agentMCPMu.RUnlock()
	endpoint, err := url.Parse(strings.TrimSpace(mcpURL))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return "", errors.New("stockv2 Agent loopback server is not configured")
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return "", ErrAgentProviderNotFound
	}
	return endpoint.Scheme + "://" + endpoint.Host +
		"/api/stockv2/agent/codex-proxy/" + url.PathEscape(providerID), nil
}

func (s *Service) handleCodexCLIResponsesProxy(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	providerID := strings.TrimSpace(r.PathValue("providerID"))
	if providerID == "" || s.store == nil {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}
	provider, err := s.store.GetAgentProviderProfile(r.Context(), providerID)
	if err != nil {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}
	if provider.ProviderType != AgentProviderTypeCodexCLI || isDefaultCodexCLIProvider(provider) {
		http.Error(w, "provider is not a custom Codex CLI provider", http.StatusForbidden)
		return
	}
	baseURL, apiKey, err := agentProviderOpenAIConfig(provider)
	if err != nil {
		http.Error(w, "provider is not configured", http.StatusUnprocessableEntity)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, codexResponsesProxyRequestMaxBytes))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	transformed, mapping, err := transformCodexResponsesRequest(body)
	if err != nil {
		http.Error(w, "invalid Responses request", http.StatusBadRequest)
		return
	}
	upstreamURL := strings.TrimRight(baseURL, "/") + "/responses"
	upstreamRequest, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, bytes.NewReader(transformed))
	if err != nil {
		http.Error(w, "provider request failed", http.StatusBadGateway)
		return
	}
	upstreamRequest.Header.Set("Authorization", "Bearer "+apiKey)
	upstreamRequest.Header.Set("Content-Type", "application/json")
	upstreamRequest.Header.Set("Accept", firstNonEmpty(strings.TrimSpace(r.Header.Get("Accept")), "text/event-stream"))
	if userAgent := strings.TrimSpace(r.Header.Get("User-Agent")); userAgent != "" {
		upstreamRequest.Header.Set("User-Agent", safelog.Text(userAgent, 256))
	}

	client := *s.agentHTTPClient()
	// The task context owns the deadline. A shared HTTP client timeout would
	// incorrectly cut off long reasoning streams before the Agent task timeout.
	client.Timeout = 0
	response, err := client.Do(upstreamRequest)
	if err != nil {
		s.logCodexResponsesProxyFailure(providerID, 0, time.Since(started), err)
		http.Error(w, "provider request failed", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()

	contentType := firstNonEmpty(strings.TrimSpace(response.Header.Get("Content-Type")), "application/json")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(response.StatusCode)

	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		err = streamTransformedCodexResponses(w, response.Body, mapping)
	} else {
		err = writeTransformedCodexResponse(w, response.Body, mapping)
	}
	duration := time.Since(started)
	if err != nil {
		s.logCodexResponsesProxyFailure(providerID, response.StatusCode, duration, err)
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		s.logCodexResponsesProxyFailure(providerID, response.StatusCode, duration, errors.New("provider returned non-success status"))
		return
	}
	if duration >= codexResponsesProxySlowRequest && s.log != nil {
		s.log.Warn(
			"stockv2 Codex Responses proxy slow request",
			"provider_id", providerID,
			"status", response.StatusCode,
			"duration_ms", duration.Milliseconds(),
		)
	}
}

func (s *Service) logCodexResponsesProxyFailure(providerID string, status int, duration time.Duration, err error) {
	if s.log == nil {
		return
	}
	s.log.Warn(
		"stockv2 Codex Responses proxy request failed",
		"provider_id", providerID,
		"status", status,
		"duration_ms", duration.Milliseconds(),
		"error", safelog.Text(err.Error(), 240),
	)
}

func transformCodexResponsesRequest(body []byte) ([]byte, map[string]codexNamespaceToolName, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return nil, nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, nil, errors.New("request must contain exactly one JSON value")
	}

	mapping := make(map[string]codexNamespaceToolName)
	if tools, ok := payload["tools"].([]any); ok {
		flattened := make([]any, 0, len(tools))
		for _, rawSpec := range tools {
			spec, ok := rawSpec.(map[string]any)
			if !ok || stringFromAny(spec["type"]) != "namespace" {
				flattened = append(flattened, rawSpec)
				continue
			}
			namespace := strings.TrimSpace(stringFromAny(spec["name"]))
			nested, ok := spec["tools"].([]any)
			if namespace == "" || !ok {
				return nil, nil, errors.New("malformed namespace tool")
			}
			for _, rawTool := range nested {
				tool, ok := rawTool.(map[string]any)
				if !ok || stringFromAny(tool["type"]) != "function" {
					return nil, nil, errors.New("namespace contains a non-function tool")
				}
				name := strings.TrimSpace(stringFromAny(tool["name"]))
				if name == "" {
					return nil, nil, errors.New("namespace contains an unnamed tool")
				}
				encoded := codexResponsesFlatToolName(namespace, name)
				original := codexNamespaceToolName{Namespace: namespace, Name: name}
				if previous, exists := mapping[encoded]; exists && previous != original {
					return nil, nil, errors.New("flattened tool name collision")
				}
				mapping[encoded] = original
				copyTool := make(map[string]any, len(tool))
				for key, value := range tool {
					copyTool[key] = value
				}
				copyTool["name"] = encoded
				flattened = append(flattened, copyTool)
			}
		}
		payload["tools"] = flattened
	}
	flattenCodexResponsesCalls(payload["input"])
	transformed, err := json.Marshal(payload)
	return transformed, mapping, err
}

func codexResponsesFlatToolName(namespace, name string) string {
	raw := strings.TrimSpace(namespace) + "__" + strings.TrimSpace(name)
	var safe strings.Builder
	safe.Grow(len(raw))
	for _, char := range raw {
		switch {
		case char >= 'a' && char <= 'z',
			char >= 'A' && char <= 'Z',
			char >= '0' && char <= '9',
			char == '_',
			char == '-':
			safe.WriteRune(char)
		default:
			safe.WriteByte('_')
		}
	}
	digest := sha256.Sum256([]byte(raw))
	suffix := "__" + hex.EncodeToString(digest[:8])
	prefix := safe.String()
	maxPrefix := codexResponsesToolNameMaxBytes - len(suffix)
	if len(prefix) > maxPrefix {
		prefix = prefix[:maxPrefix]
	}
	if prefix == "" {
		prefix = "tool"
	}
	return prefix + suffix
}

func flattenCodexResponsesCalls(value any) {
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			flattenCodexResponsesCalls(child)
		}
	case map[string]any:
		itemType := stringFromAny(typed["type"])
		if itemType == "function_call" || itemType == "custom_tool_call" {
			namespace := strings.TrimSpace(stringFromAny(typed["namespace"]))
			name := strings.TrimSpace(stringFromAny(typed["name"]))
			if namespace != "" && name != "" {
				typed["name"] = codexResponsesFlatToolName(namespace, name)
				delete(typed, "namespace")
			}
		}
		for _, child := range typed {
			flattenCodexResponsesCalls(child)
		}
	}
}

func restoreCodexResponsesCalls(value any, mapping map[string]codexNamespaceToolName) {
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			restoreCodexResponsesCalls(child, mapping)
		}
	case map[string]any:
		itemType := stringFromAny(typed["type"])
		if itemType == "function_call" || itemType == "custom_tool_call" {
			if original, ok := mapping[stringFromAny(typed["name"])]; ok {
				typed["name"] = original.Name
				typed["namespace"] = original.Namespace
			}
		}
		for _, child := range typed {
			restoreCodexResponsesCalls(child, mapping)
		}
	}
}

func streamTransformedCodexResponses(w http.ResponseWriter, body io.Reader, mapping map[string]codexNamespaceToolName) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), codexResponsesProxyEventMaxBytes)
	flusher, _ := w.(http.Flusher)
	for scanner.Scan() {
		line := scanner.Bytes()
		if bytes.HasPrefix(line, []byte("data:")) {
			raw := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if len(raw) > 0 && !bytes.Equal(raw, []byte("[DONE]")) {
				transformed, err := transformCodexResponsesPayload(raw, mapping)
				if err != nil {
					return err
				}
				line = append([]byte("data: "), transformed...)
			}
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	return scanner.Err()
}

func writeTransformedCodexResponse(w http.ResponseWriter, body io.Reader, mapping map[string]codexNamespaceToolName) error {
	raw, err := io.ReadAll(io.LimitReader(body, codexResponsesProxyRequestMaxBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > codexResponsesProxyRequestMaxBytes {
		return errors.New("provider response too large")
	}
	transformed, err := transformCodexResponsesPayload(raw, mapping)
	if err != nil {
		return err
	}
	_, err = w.Write(transformed)
	return err
}

func transformCodexResponsesPayload(raw []byte, mapping map[string]codexNamespaceToolName) ([]byte, error) {
	if len(mapping) == 0 {
		return raw, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode provider response: %w", err)
	}
	restoreCodexResponsesCalls(payload, mapping)
	return json.Marshal(payload)
}
