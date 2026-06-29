package httpapi

import (
	"errors"
	"slices"
	"strconv"
)

func stockV2PositiveInt(raw string, fallback int) (int, error) {
	return stockV2MinInt(raw, fallback, 1, "invalid positive integer")
}

func stockV2NonNegativeInt(raw string, fallback int) (int, error) {
	return stockV2MinInt(raw, fallback, 0, "invalid non-negative integer")
}

func stockV2MinInt(raw string, fallback, min int, message string) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min {
		return 0, errors.New(message)
	}
	return value, nil
}

func stockV2HTTPValueIn(value string, valid ...string) bool {
	return slices.Contains(valid, value)
}
