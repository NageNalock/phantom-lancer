package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 1
	argonSaltBytes   = 16
	argonKeyBytes    = 32
)

func HashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", errors.New("密码至少需要 8 个字符")
	}
	salt := make([]byte, argonSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyBytes)
	return fmt.Sprintf(
		"argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return false
	}

	params := strings.Split(parts[2], ",")
	if len(params) != 3 {
		return false
	}
	memory, ok := trimParse(params[0], "m=")
	if !ok {
		return false
	}
	iterations, ok := trimParse(params[1], "t=")
	if !ok {
		return false
	}
	parallelism, ok := trimParse(params[2], "p=")
	if !ok {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}

	actual := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(parallelism), uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func trimParse(value, prefix string) (int, bool) {
	raw := strings.TrimPrefix(value, prefix)
	if raw == value {
		return 0, false
	}
	parsed, err := strconv.Atoi(raw)
	return parsed, err == nil
}
