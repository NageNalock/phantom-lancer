package codexgateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func StreamChatFromResponses(w http.ResponseWriter, body io.Reader, model string) (Usage, error) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	id := "chatcmpl_" + fmt.Sprintf("%x", time.Now().UnixNano())
	var usage Usage
	var sawText bool
	err := ScanSSE(body, func(event, data string) error {
		if strings.TrimSpace(data) == "" || strings.TrimSpace(data) == "[DONE]" {
			return nil
		}
		var payload any
		if json.Unmarshal([]byte(data), &payload) != nil {
			return nil
		}
		switch event {
		case "response.output_text.delta":
			if delta := extractDelta(payload); delta != "" {
				sawText = true
				if err := writeSSEData(w, BuildChatChunk(id, model, delta, nil)); err != nil {
					return err
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
		case "response.completed":
			if u := usageFromEvent(payload); u.TotalTokens > 0 || u.PromptTokens > 0 || u.CompletionTokens > 0 {
				usage = u
			}
			if !sawText {
				if text := outputTextFromEvent(payload); text != "" {
					if err := writeSSEData(w, BuildChatChunk(id, model, text, nil)); err != nil {
						return err
					}
				}
			}
		case "response.failed", "error":
			return fmt.Errorf("upstream stream failed")
		}
		return nil
	})
	stop := "stop"
	_ = writeSSEData(w, BuildChatChunk(id, model, "", &stop))
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	return usage, err
}

func RelaySSE(w http.ResponseWriter, body io.Reader) (Usage, error) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	var usage Usage
	err := ScanSSE(body, func(event, data string) error {
		if event != "" {
			if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
				return err
			}
		}
		for _, line := range strings.Split(data, "\n") {
			if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		var payload any
		if json.Unmarshal([]byte(data), &payload) == nil {
			if u := usageFromEvent(payload); u.TotalTokens > 0 || u.PromptTokens > 0 || u.CompletionTokens > 0 {
				usage = u
			}
		}
		return nil
	})
	return usage, err
}

func CollectTextFromResponses(body io.Reader) (string, Usage, error) {
	data, err := io.ReadAll(io.LimitReader(body, 32<<20))
	if err != nil {
		return "", Usage{}, err
	}
	text := strings.TrimSpace(string(data))
	if strings.HasPrefix(text, "event:") || strings.HasPrefix(text, "data:") {
		var builder strings.Builder
		var usage Usage
		err := ScanSSE(strings.NewReader(text), func(event, data string) error {
			if strings.TrimSpace(data) == "" || strings.TrimSpace(data) == "[DONE]" {
				return nil
			}
			var payload any
			if json.Unmarshal([]byte(data), &payload) != nil {
				return nil
			}
			switch event {
			case "response.output_text.delta":
				builder.WriteString(extractDelta(payload))
			case "response.completed":
				if builder.Len() == 0 {
					builder.WriteString(outputTextFromEvent(payload))
				}
				if u := usageFromEvent(payload); u.TotalTokens > 0 || u.PromptTokens > 0 || u.CompletionTokens > 0 {
					usage = u
				}
			}
			return nil
		})
		return builder.String(), usage, err
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", Usage{}, err
	}
	return outputTextFromEvent(payload), usageFromEvent(payload), nil
}

func ScanSSE(body io.Reader, fn func(event, data string) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var event string
	data := []string{}
	flush := func() error {
		if event == "" && len(data) == 0 {
			return nil
		}
		err := fn(event, strings.Join(data, "\n"))
		event = ""
		data = nil
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

func writeSSEData(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", string(data))
	return err
}

func outputTextFromEvent(value any) string {
	data, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	if text, ok := data["output_text"].(string); ok {
		return text
	}
	if response, ok := data["response"].(map[string]any); ok {
		if text, ok := response["output_text"].(string); ok {
			return text
		}
		if output, ok := response["output"].([]any); ok {
			return textFromOutput(output)
		}
	}
	if output, ok := data["output"].([]any); ok {
		return textFromOutput(output)
	}
	return ""
}

func textFromOutput(output []any) string {
	var builder strings.Builder
	for _, item := range output {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := record["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			partRecord, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := partRecord["text"].(string); ok {
				builder.WriteString(text)
			}
		}
	}
	return builder.String()
}

func extractDelta(value any) string {
	data, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"delta", "text"} {
		if text, ok := data[key].(string); ok {
			return text
		}
	}
	if item, ok := data["item"].(map[string]any); ok {
		if text, ok := item["text"].(string); ok {
			return text
		}
	}
	return ""
}

func usageFromEvent(value any) Usage {
	data, ok := value.(map[string]any)
	if !ok {
		return Usage{}
	}
	if usage, ok := data["usage"].(map[string]any); ok {
		return UsageFromResponses(usage)
	}
	if response, ok := data["response"].(map[string]any); ok {
		if usage, ok := response["usage"].(map[string]any); ok {
			return UsageFromResponses(usage)
		}
	}
	return Usage{}
}

type UsageCaptureWriter struct {
	http.ResponseWriter
	buffer strings.Builder
}

func (w *UsageCaptureWriter) Write(p []byte) (int, error) {
	if w.buffer.Len() < 4<<20 {
		_, _ = w.buffer.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *UsageCaptureWriter) Usage() Usage {
	var payload any
	if json.Unmarshal([]byte(w.buffer.String()), &payload) != nil {
		return Usage{}
	}
	return usageFromEvent(payload)
}
