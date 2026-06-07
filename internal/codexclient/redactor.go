package codexclient

import (
	"regexp"
	"strings"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s"',]+`),
	regexp.MustCompile(`(?i)((?:access|refresh|id)[_-]?token\s*["']?\s*[:=]\s*["']?)[^"',\s&}]+`),
	regexp.MustCompile(`(?i)((?:api[_-]?key|password|passwd|secret|cookie)\s*["']?\s*[:=]\s*["']?)[^"',\s&}]+`),
	regexp.MustCompile(`(?i)(eyJ[a-z0-9_-]{6,})\.[a-z0-9_-]{6,}\.[a-z0-9_-]{6,}`),
}

// Redact removes secret-like substrings and trims a message to a bounded length.
// It is used before any error or preview is persisted, logged or returned.
func Redact(message string, maxRunes int) string {
	message = strings.TrimSpace(message)
	for _, pattern := range secretPatterns {
		message = pattern.ReplaceAllString(message, "${1}[redacted]")
	}
	message = strings.Join(strings.Fields(message), " ")
	if maxRunes > 0 {
		runes := []rune(message)
		if len(runes) > maxRunes {
			message = string(runes[:maxRunes]) + "…"
		}
	}
	return message
}

// Preview produces a redacted, length-bounded preview of free text for storage
// in text_preview, prompt_summary, command previews and similar columns.
func Preview(value string, maxRunes int) string {
	return Redact(value, maxRunes)
}
