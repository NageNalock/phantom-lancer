package ids

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

func New(prefix string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	encoded := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf))
	if prefix == "" {
		return encoded, nil
	}
	return prefix + "_" + encoded, nil
}
