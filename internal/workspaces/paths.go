package workspaces

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrPathOutOfBoundary = errors.New("path is outside allowed roots")
var ErrPathNotFound = errors.New("path does not exist")
var ErrPathNotDirectory = errors.New("path is not a directory")

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

func NormalizeWorkspacePath(allowedRoots []string, value string) (string, error) {
	if value == "" {
		return "", errors.New("路径不能为空")
	}
	normalized, err := normalizeExistingDir(value)
	if err != nil {
		return "", err
	}
	for _, root := range allowedRoots {
		if isInsideOrEqual(root, normalized) {
			return normalized, nil
		}
	}
	return "", ErrPathOutOfBoundary
}

func NormalizeWorkspacePathForCreate(allowedRoots []string, value string) (string, error) {
	if value == "" {
		return "", errors.New("路径不能为空")
	}
	normalized, err := NormalizeWorkspacePath(allowedRoots, value)
	if err == nil {
		return normalized, nil
	}
	if !errors.Is(err, ErrPathNotFound) {
		return "", err
	}

	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	target := filepath.Clean(abs)
	if isSensitiveRoot(target) {
		return "", errors.New("不允许使用系统敏感路径")
	}
	ancestor, rel, err := deepestExistingAncestor(target)
	if err != nil {
		return "", err
	}
	resolvedAncestor, err := normalizeExistingDir(ancestor)
	if err != nil {
		return "", err
	}
	resolvedTarget := filepath.Clean(filepath.Join(resolvedAncestor, rel))
	if isSensitiveRoot(resolvedTarget) {
		return "", errors.New("不允许使用系统敏感路径")
	}
	for _, root := range allowedRoots {
		if isInsideOrEqual(root, resolvedTarget) {
			return resolvedTarget, nil
		}
	}
	return "", ErrPathOutOfBoundary
}

func IsGitRepository(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
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

func deepestExistingAncestor(path string) (string, string, error) {
	current := path
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return "", "", fmt.Errorf("%w: %s", ErrPathNotDirectory, current)
			}
			rel, err := filepath.Rel(current, path)
			if err != nil {
				return "", "", err
			}
			return current, rel, nil
		}
		if !os.IsNotExist(err) {
			return "", "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", fmt.Errorf("%w: %s", ErrPathNotFound, path)
		}
		current = parent
	}
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
