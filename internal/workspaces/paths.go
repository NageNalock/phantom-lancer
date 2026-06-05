package workspaces

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrPathOutOfBoundary = errors.New("path is outside allowed roots")

func NormalizeAllowedRoots(roots []string) ([]string, error) {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		normalized, err := normalizeExisting(root)
		if err != nil {
			return nil, err
		}
		out = append(out, normalized)
	}
	return out, nil
}

func NormalizeWorkspacePath(allowedRoots []string, value string) (string, error) {
	if value == "" {
		return "", errors.New("路径不能为空")
	}
	normalized, err := normalizeExisting(value)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(normalized)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("路径必须是目录")
	}
	for _, root := range allowedRoots {
		if isInsideOrEqual(root, normalized) {
			return normalized, nil
		}
	}
	return "", ErrPathOutOfBoundary
}

func IsGitRepository(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && info.IsDir()
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

func isInsideOrEqual(root, target string) bool {
	if root == target {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

func isSensitiveRoot(path string) bool {
	switch path {
	case "/", "/etc", "/var", "/home", "/root", "/usr", "/bin", "/sbin":
		return true
	default:
		return false
	}
}
