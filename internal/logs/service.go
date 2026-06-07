package logs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/config"
	"phantom-lancer/internal/events"
	"phantom-lancer/internal/storage"
)

const (
	serviceSourceID = "service.phantom"
	v2raySourceID   = "event.v2ray.default"
)

var ErrSourceNotFound = errors.New("log source not found")

type Service struct {
	Store *storage.Store
	Cfg   config.Config
}

type Source struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Module          string `json:"module"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Path            string `json:"path,omitempty"`
	Status          string `json:"status"`
	Managed         bool   `json:"managed"`
	SizeBytes       int64  `json:"sizeBytes,omitempty"`
	UpdatedAt       string `json:"updatedAt,omitempty"`
	ErrorCount      int    `json:"errorCount"`
	WarningCount    int    `json:"warningCount"`
	RotationSummary string `json:"rotationSummary,omitempty"`
}

type TailOptions struct {
	Limit    int
	MaxBytes int64
	Level    string
	Query    string
}

type TailResponse struct {
	Source    Source    `json:"source"`
	Lines     []LogLine `json:"lines"`
	Limit     int       `json:"limit"`
	MaxBytes  int64     `json:"maxBytes"`
	Truncated bool      `json:"truncated"`
	Cursor    string    `json:"cursor,omitempty"`
}

type LogLine struct {
	SourceID string         `json:"sourceId"`
	Offset   int64          `json:"offset"`
	Time     string         `json:"time,omitempty"`
	Level    string         `json:"level"`
	Message  string         `json:"message"`
	Fields   map[string]any `json:"fields,omitempty"`
	Raw      string         `json:"raw"`
	Redacted bool           `json:"redacted,omitempty"`
}

func NewService(store *storage.Store, cfg config.Config) *Service {
	return &Service{Store: store, Cfg: cfg}
}

func (s *Service) ListSources(ctx context.Context) ([]Source, error) {
	sources := []Source{s.serviceFileSource()}

	v2rayEvents, err := s.Store.ListEvents(ctx, "v2ray_service", "default", 0, 200)
	if err == nil {
		source := eventSource(v2raySourceID, "v2ray", "V2Ray runtime", "内嵌 V2Ray 运行事件", v2rayEvents)
		sources = append(sources, source)
	}

	return sources, nil
}

func (s *Service) Tail(ctx context.Context, sourceID string, opts TailOptions) (TailResponse, error) {
	opts = normalizeOptions(opts)
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == serviceSourceID {
		source := s.serviceFileSource()
		lines, cursor, truncated, err := readFileTail(source.Path, source.ID, opts)
		if err != nil {
			if os.IsNotExist(err) {
				return TailResponse{Source: source, Lines: []LogLine{}, Limit: opts.Limit, MaxBytes: opts.MaxBytes}, nil
			}
			return TailResponse{}, err
		}
		source = withCounts(source, lines)
		return TailResponse{Source: source, Lines: lines, Limit: opts.Limit, MaxBytes: opts.MaxBytes, Truncated: truncated, Cursor: cursor}, nil
	}
	if sourceID == v2raySourceID {
		events, err := s.Store.ListEvents(ctx, "v2ray_service", "default", 0, 500)
		if err != nil {
			return TailResponse{}, err
		}
		lines := eventLines(sourceID, events, opts)
		source := eventSource(v2raySourceID, "v2ray", "V2Ray runtime", "内嵌 V2Ray 运行事件", events)
		return TailResponse{Source: source, Lines: lines, Limit: opts.Limit, MaxBytes: opts.MaxBytes}, nil
	}
	return TailResponse{}, ErrSourceNotFound
}

func (s *Service) serviceFileSource() Source {
	source := Source{
		ID:              serviceSourceID,
		Kind:            "file",
		Module:          "phantom",
		Name:            "phantom-lancer.jsonl",
		Description:     "Go 服务 structured JSONL 日志",
		Path:            s.Cfg.LogFile,
		Status:          "missing",
		Managed:         true,
		RotationSummary: fmt.Sprintf("%dMB / %d files / %d days", s.Cfg.LogMaxSizeMB, s.Cfg.LogMaxFiles, s.Cfg.LogMaxAgeDays),
	}
	info, err := os.Stat(s.Cfg.LogFile)
	if err != nil {
		if os.IsNotExist(err) {
			return source
		}
		source.Status = "unreadable"
		return source
	}
	source.Status = "available"
	source.SizeBytes = info.Size()
	source.UpdatedAt = info.ModTime().UTC().Format(time.RFC3339Nano)
	return source
}

func readFileTail(pathValue, sourceID string, opts TailOptions) ([]LogLine, string, bool, error) {
	pathValue = filepath.Clean(pathValue)
	file, err := os.Open(pathValue)
	if err != nil {
		return nil, "", false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, "", false, err
	}
	size := info.Size()
	start := int64(0)
	truncated := false
	if opts.MaxBytes > 0 && size > opts.MaxBytes {
		start = size - opts.MaxBytes
		truncated = true
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, "", false, err
	}
	data, err := io.ReadAll(io.LimitReader(file, opts.MaxBytes+1))
	if err != nil {
		return nil, "", false, err
	}
	if int64(len(data)) > opts.MaxBytes {
		data = data[:opts.MaxBytes]
		truncated = true
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if start > 0 {
		if index := strings.IndexByte(text, '\n'); index >= 0 {
			start += int64(index + 1)
			text = text[index+1:]
		}
	}
	rawLines := strings.Split(text, "\n")
	lines := make([]LogLine, 0, len(rawLines))
	offset := start
	for _, raw := range rawLines {
		if raw == "" {
			offset++
			continue
		}
		line := ParseLine(sourceID, offset, raw)
		offset += int64(len(raw) + 1)
		if lineMatches(line, opts) {
			lines = append(lines, line)
		}
	}
	if len(lines) > opts.Limit {
		lines = lines[len(lines)-opts.Limit:]
	}
	cursor := fmt.Sprintf("%d:%d", size, info.ModTime().UnixNano())
	return lines, cursor, truncated, nil
}

func eventSource(id, module, name, description string, items []events.Event) Source {
	source := Source{
		ID:          id,
		Kind:        "event",
		Module:      module,
		Name:        name,
		Description: description,
		Status:      "available",
		Managed:     true,
	}
	if len(items) == 0 {
		source.Status = "empty"
		return source
	}
	source.UpdatedAt = items[len(items)-1].CreatedAt
	lines := eventLines(id, items, TailOptions{Limit: 200, MaxBytes: 256 * 1024})
	source = withCounts(source, lines)
	return source
}

func eventLines(sourceID string, items []events.Event, opts TailOptions) []LogLine {
	opts = normalizeOptions(opts)
	lines := make([]LogLine, 0, len(items))
	for _, item := range items {
		line := eventLine(sourceID, item)
		if lineMatches(line, opts) {
			lines = append(lines, line)
		}
	}
	if len(lines) > opts.Limit {
		lines = lines[len(lines)-opts.Limit:]
	}
	return lines
}

func eventLine(sourceID string, item events.Event) LogLine {
	fields := redactMap(item.Payload)
	message := messageFromEvent(item.Type, fields)
	rawPayload, _ := json.Marshal(fields)
	return LogLine{
		SourceID: sourceID,
		Offset:   item.Sequence,
		Time:     item.CreatedAt,
		Level:    levelFromEvent(item.Type, message),
		Message:  RedactString(message),
		Fields: map[string]any{
			"type":    item.Type,
			"payload": fields,
		},
		Raw:      string(rawPayload),
		Redacted: true,
	}
}

func ParseLine(sourceID string, offset int64, raw string) LogLine {
	line := LogLine{
		SourceID: sourceID,
		Offset:   offset,
		Level:    "info",
		Message:  RedactString(raw),
		Raw:      RedactString(raw),
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err == nil {
		payload = redactMap(payload)
		line.Fields = payload
		line.Time = stringField(payload, "time", "ts", "timestamp", "createdAt")
		line.Level = normalizeLevel(stringField(payload, "level", "severity"))
		if line.Level == "" {
			line.Level = levelFromText(raw)
		}
		line.Message = RedactString(firstNonEmpty(stringField(payload, "msg", "message"), raw))
		if encoded, err := json.Marshal(payload); err == nil {
			line.Raw = string(encoded)
		}
		line.Redacted = true
		return line
	}
	line.Level = levelFromText(raw)
	return line
}

func normalizeOptions(opts TailOptions) TailOptions {
	if opts.Limit <= 0 {
		opts.Limit = 200
	}
	if opts.Limit > 1000 {
		opts.Limit = 1000
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 256 * 1024
	}
	if opts.MaxBytes > 4*1024*1024 {
		opts.MaxBytes = 4 * 1024 * 1024
	}
	opts.Level = normalizeLevel(opts.Level)
	opts.Query = strings.TrimSpace(opts.Query)
	return opts
}

func lineMatches(line LogLine, opts TailOptions) bool {
	if opts.Level != "" && opts.Level != "all" && line.Level != opts.Level {
		return false
	}
	if opts.Query == "" {
		return true
	}
	needle := strings.ToLower(opts.Query)
	return strings.Contains(strings.ToLower(line.Message), needle) || strings.Contains(strings.ToLower(line.Raw), needle)
}

func withCounts(source Source, lines []LogLine) Source {
	for _, line := range lines {
		switch line.Level {
		case "error":
			source.ErrorCount++
		case "warn":
			source.WarningCount++
		}
	}
	return source
}

func levelFromEvent(eventType, message string) string {
	value := strings.ToLower(eventType + " " + message)
	if strings.Contains(value, "failed") || strings.Contains(value, "error") || strings.Contains(value, "stderr") || strings.Contains(value, "panic") {
		return "error"
	}
	if strings.Contains(value, "warn") || strings.Contains(value, "interrupted") || strings.Contains(value, "stale") {
		return "warn"
	}
	return "info"
}

func levelFromText(value string) string {
	lower := strings.ToLower(value)
	if strings.Contains(lower, " error ") || strings.Contains(lower, `"level":"error"`) || strings.Contains(lower, "panic") || strings.Contains(lower, "failed") {
		return "error"
	}
	if strings.Contains(lower, " warn") || strings.Contains(lower, `"level":"warn"`) || strings.Contains(lower, "warning") {
		return "warn"
	}
	return "info"
}

func normalizeLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "error", "err", "fatal", "panic":
		return "error"
	case "warn", "warning":
		return "warn"
	case "info", "debug", "trace":
		return "info"
	case "all":
		return "all"
	default:
		return ""
	}
}

func messageFromEvent(eventType string, payload map[string]any) string {
	for _, key := range []string{"message", "line", "reason", "summary", "error"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return eventType
}

func stringField(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func redactMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		lower := strings.ToLower(key)
		if sensitiveKey(lower) {
			out[key] = "[redacted]"
			continue
		}
		out[key] = redactValue(value)
	}
	return out
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case string:
		return RedactString(typed)
	case map[string]any:
		return redactMap(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, redactValue(item))
		}
		return out
	default:
		return typed
	}
}

func sensitiveKey(key string) bool {
	for _, marker := range []string{"password", "token", "secret", "api_key", "apikey", "authorization", "cookie", "session", "csrf", "key"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

var (
	bearerPattern   = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	keyValuePattern = regexp.MustCompile(`(?i)\b(api[_-]?key|token|secret|password|authorization|cookie|csrf|session)\b(\s*[=:]\s*)("[^"]+"|'[^']+'|[^\s,;]+)`)
	dataURLPattern  = regexp.MustCompile(`data:image/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/=_-]+`)
	uuidPattern     = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
	urlPattern      = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

func RedactString(value string) string {
	if value == "" {
		return value
	}
	redacted := bearerPattern.ReplaceAllString(value, "Bearer [redacted]")
	redacted = keyValuePattern.ReplaceAllString(redacted, "$1$2[redacted]")
	redacted = dataURLPattern.ReplaceAllString(redacted, "[redacted-image-data]")
	redacted = uuidPattern.ReplaceAllStringFunc(redacted, func(match string) string {
		if len(match) <= 8 {
			return "****"
		}
		return match[:4] + "..." + match[len(match)-4:]
	})
	redacted = urlPattern.ReplaceAllStringFunc(redacted, func(match string) string {
		parsed, err := url.Parse(match)
		if err != nil || parsed.RawQuery == "" {
			return match
		}
		parsed.RawQuery = "redacted"
		return parsed.String()
	})
	if len(redacted) > 4096 {
		redacted = redacted[:4096] + "...[truncated]"
	}
	return redacted
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func shortID(value string) string {
	if len(value) <= 10 {
		return value
	}
	return value[:10]
}

func preview(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

func ParseLimit(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}

func ParseMaxBytes(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func SortSources(items []Source) {
	slices.SortFunc(items, func(a, b Source) int {
		if a.Kind != b.Kind {
			if a.Kind == "file" {
				return -1
			}
			if b.Kind == "file" {
				return 1
			}
		}
		return strings.Compare(a.Name, b.Name)
	})
}
