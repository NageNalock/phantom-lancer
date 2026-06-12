package httpserver

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// ValidateTLSPaths 执行完整的路径安全+证书校验（M5 安全要求）。
//
// 校验项：
//  1. 两路径均非空
//  2. 拒绝 NUL 字节注入
//  3. filepath.Clean + filepath.Abs 得绝对路径
//  4. EvalSymlinks 检测并拒绝符号链接（防止 symlink 攻击）
//  5. os.Lstat 检查：必须是常规文件（拒绝目录/device/FIFO）
//  6. key 权限：Mode().Perm() & 0o002 == 0（world writable 禁止）
//  7. owner uid 检查：st_uid == os.Geteuid()（checkOwner=true 时执行，Unix 平台）
//  8. tls.LoadX509KeyPair 能成功解析，且能提取 leaf 证书
//
// 返回经过 EvalSymlinks 处理的干净路径，以及解析出的 leaf 证书。
func ValidateTLSPaths(certPath, keyPath string, checkOwner bool) (cleanCert, cleanKey string, leaf *x509.Certificate, err error) {
	if strings.TrimSpace(certPath) == "" || strings.TrimSpace(keyPath) == "" {
		return "", "", nil, errors.New("empty_path: both cert path and key path are required")
	}
	if strings.ContainsAny(certPath, "\x00") || strings.ContainsAny(keyPath, "\x00") {
		return "", "", nil, errors.New("invalid_path: path contains NUL byte")
	}

	var certLeaf *x509.Certificate
	cleanCert, certLeaf, err = validateOneTLSPath(certPath, roleCertificate, checkOwner)
	if err != nil {
		return "", "", nil, err
	}
	cleanKey, _, err = validateOneTLSPath(keyPath, rolePrivateKey, checkOwner)
	if err != nil {
		return "", "", nil, err
	}

	// 8. 解析 keypair
	pair, err := tls.LoadX509KeyPair(cleanCert, cleanKey)
	if err != nil {
		return "", "", nil, fmt.Errorf("load_key_pair_failed: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return "", "", nil, errors.New("load_key_pair_failed: certificate chain empty")
	}
	if certLeaf == nil {
		if parsed, perr := x509.ParseCertificate(pair.Certificate[0]); perr == nil {
			certLeaf = parsed
		}
	}
	return cleanCert, cleanKey, certLeaf, nil
}

type pathRole int

const (
	roleCertificate pathRole = 1
	rolePrivateKey  pathRole = 2
)

// validateOneTLSPath 执行单路径的安全检查（1–7 中的路径相关项）。
func validateOneTLSPath(raw string, role pathRole, checkOwner bool) (string, *x509.Certificate, error) {
	cleaned, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", nil, fmt.Errorf("invalid_path: %w", err)
	}

	// Lstat 检查最后一段：不能是 symlink，同时也确认文件存在
	lst, lerr := os.Lstat(cleaned)
	if lerr != nil {
		return "", nil, fmt.Errorf("stat_failed: %q: %w", cleaned, lerr)
	}
	if lst.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("symlink_not_allowed: %q is a symlink", cleaned)
	}

	// EvalSymlinks 处理中间段——若结果不一致说明路径中某段是 symlink
	resolved, rerr := filepath.EvalSymlinks(cleaned)
	if rerr != nil {
		return "", nil, fmt.Errorf("resolve_symlinks_failed: %w", rerr)
	}
	// macOS: /var/ -> /private/var/ 是系统级不可避免的 bindfs，不应视为攻击。
	// 仅比较 EvalSymlinks 前后经 filepath.Clean 的最后一段与父目录段，忽略
	// 系统级绝对路径规范化导致的 /private 前缀差异。
	if pathsDifferBeyondPrivatePrefix(cleaned, resolved) {
		return "", nil, fmt.Errorf("symlink_not_allowed: %q resolves to %q via symlink", cleaned, resolved)
	}
	cleaned = resolved

	// 必须是常规文件
	info, serr := os.Stat(cleaned)
	if serr != nil {
		return "", nil, fmt.Errorf("stat_failed: %q: %w", cleaned, serr)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("not_regular_file: %q has mode %s", cleaned, info.Mode().String())
	}

	// 私钥专属检查
	if role == rolePrivateKey {
		if info.Mode().Perm()&0o002 != 0 {
			return "", nil, fmt.Errorf("key_file_world_writable: mode=%o", info.Mode().Perm())
		}
		if checkOwner {
			if err := checkPrivateKeyOwner(cleaned); err != nil {
				return "", nil, err
			}
		}
	}

	// 证书：尝试提前解析 leaf 提供给上层（失败不报错，留给 LoadX509KeyPair）
	var leaf *x509.Certificate
	if role == roleCertificate {
		if data, rerr := os.ReadFile(cleaned); rerr == nil {
			block, _ := pem.Decode(data)
			if block != nil && block.Type == "CERTIFICATE" {
				if parsed, perr := x509.ParseCertificate(block.Bytes); perr == nil {
					leaf = parsed
				}
			}
		}
	}
	return cleaned, leaf, nil
}

// checkPrivateKeyOwner 在 Unix 系统比较 st_uid 与当前进程 euid。
// Windows 等平台默认跳过（文件权限语义不同）。
func checkPrivateKeyOwner(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("owner_check_failed: %w", err)
	}
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// 未知文件系统，跳过 owner 校验（保守放行）
		return nil
	}
	euid := os.Geteuid()
	if int(sys.Uid) != euid {
		return fmt.Errorf("key_file_owner_mismatch: file owner uid=%d, process euid=%d", sys.Uid, euid)
	}
	return nil
}

// pathsDifferBeyondPrivatePrefix 比较 EvalSymlinks 前后两个绝对路径，
// 仅容忍 macOS 系统级 /var → /private/var 规范化差异，其他 symlink
// 跳转一律视为不同（返回 true）。
func pathsDifferBeyondPrivatePrefix(cleaned, resolved string) bool {
	// 完全相等：无需处理
	if cleaned == resolved {
		return false
	}
	// macOS: /var/X/Y/z → EvalSymlinks → /private/var/X/Y/z。
	// 比较 stripped 后剩余部分是否一致。
	const macosPrivatePrefix = "/private"
	a := strings.TrimSuffix(filepath.Clean(cleaned), string(filepath.Separator))
	b := strings.TrimSuffix(filepath.Clean(resolved), string(filepath.Separator))
	if strings.HasPrefix(b, macosPrivatePrefix+string(filepath.Separator)) {
		bStripped := b[len(macosPrivatePrefix):]
		if a == bStripped {
			return false
		}
	}
	if strings.HasPrefix(a, macosPrivatePrefix+string(filepath.Separator)) {
		aStripped := a[len(macosPrivatePrefix):]
		if aStripped == b {
			return false
		}
	}
	return true
}
