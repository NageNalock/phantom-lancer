package safelog

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s"',]+`),
	regexp.MustCompile(`(?i)((?:access|refresh|id)[_-]?token\s*["']?\s*[:=]\s*["']?)[^"',\s&}]+`),
	regexp.MustCompile(`(?i)((?:api[_-]?key|password|passwd|secret)\s*["']?\s*[:=]\s*["']?)[^"',\s&}]+`),
	regexp.MustCompile(`(?i)((?:X-Amz-Signature|X-Amz-Credential|X-Amz-Security-Token)=)[^&\s]+`),
}

func Error(err error, max int) string {
	if err == nil {
		return ""
	}
	return Text(err.Error(), max)
}

func Text(value string, max int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllString(value, "${1}[REDACTED]")
	}
	if max <= 0 {
		max = 240
	}
	runes := []rune(value)
	if len(runes) > max {
		return string(runes[:max]) + "..."
	}
	return value
}

func URLLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return Text(raw, 120)
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path == "" {
		return parsed.Scheme + "://" + parsed.Host
	}
	return Text(fmt.Sprintf("%s://%s%s", parsed.Scheme, parsed.Host, path), 160)
}

func HostLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return Text(raw, 80)
	}
	return parsed.Host
}
