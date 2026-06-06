package selfupdate

import (
	"strconv"
	"strings"
)

func compareVersions(current, latest string) (int, bool) {
	currentParts, ok := parseVersion(current)
	if !ok {
		return 0, false
	}
	latestParts, ok := parseVersion(latest)
	if !ok {
		return 0, false
	}
	for index := 0; index < 3; index++ {
		if currentParts[index] < latestParts[index] {
			return -1, true
		}
		if currentParts[index] > latestParts[index] {
			return 1, true
		}
	}
	return 0, true
}

func parseVersion(value string) ([3]int, bool) {
	var out [3]int
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "v")
	if value == "" || value == "dev" || strings.Contains(value, "-dev") {
		return out, false
	}
	if before, _, ok := strings.Cut(value, "+"); ok {
		value = before
	}
	if before, _, ok := strings.Cut(value, "-"); ok {
		value = before
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return out, false
	}
	for index, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return out, false
		}
		out[index] = parsed
	}
	return out, true
}
