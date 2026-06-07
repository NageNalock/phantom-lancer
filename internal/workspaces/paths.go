package workspaces

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrPathNotFound = errors.New("path does not exist")
var ErrPathNotDirectory = errors.New("path is not a directory")

// NormalizeAllowedRoots validates and canonicalizes the globally allowed root
// directories used to bound filesystem access for the console.
func NormalizeAllowedRoots(roots []string) ([]string, error) {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		normalized, err := normalizeExistingDir(root)
		if err != nil {
			if errors.Is(err, ErrPathNotFound) {
				return nil, fmt.Errorf("允许根目录不存在：%s", root)
			}
			if errors.Is(err, ErrPathNotDirectory) {
				return nil, fmt.Errorf("允许根目录不是目录：%s", root)
			}
			return nil, fmt.Errorf("允许根目录无效：%s: %w", root, err)
		}
		out = append(out, normalized)
	}
	return out, nil
}

func normalizeExistingDir(value string) (string, error) {
	normalized, err := normalizeExisting(value)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrPathNotFound, value)
		}
		return "", err
	}
	info, err := os.Stat(normalized)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrPathNotFound, value)
		}
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrPathNotDirectory, value)
	}
	return normalized, nil
}

func normalizeExisting(value string) (string, error) {
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(resolved)
	if isSensitiveRoot(cleaned) {
		return "", errors.New("不允许使用系统敏感路径")
	}
	return cleaned, nil
}

func isSensitiveRoot(path string) bool {
	switch path {
	case "/", "/etc", "/var", "/home", "/root", "/usr", "/bin", "/sbin":
		return true
	default:
		return false
	}
}
