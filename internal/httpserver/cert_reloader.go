package httpserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// reloadInterval 控制后台证书自动 reload 的频率。
// 作为包级 var 导出，便于测试时替换。
var reloadInterval = 10 * time.Second

// TLSStatus 是 CertReloader 对外暴露的只读快照，供 Endpoint() 序列化到前端。
type TLSStatus struct {
	DNSNames     []string
	NotBefore    time.Time
	NotAfter     time.Time
	LastError    string
	Issuer       string
	Subject      string
	SerialNumber string
	SigAlg       string
}

// CertReloader 在不中断 listener 的情况下按需加载 TLS 证书。
//
// 设计约束（M4 严格模式）：
//   - GetCertificate 热路径绝不做任何 I/O、加锁、syscall——只从 atomic.Value 读。
//   - 文件读取、解析、权限检查统一在 LoadNow（由 ticker 或 API 调用者触发）里执行。
//   - 失败限频：同一 lastErr 文本 60s 内只打一次 WARN。
//   - 部分写入保护（G6）：LoadX509KeyPair 失败不立刻覆盖 lastErr，需连续失败 3 次才记录，
//     容忍 certbot 等工具写入 cert 和 key 之间的短暂窗口。
type CertReloader struct {
	certPath, keyPath string

	cert atomic.Value // *tls.Certificate

	mu               sync.RWMutex
	lastErr          error
	lastErrLoggedAt  time.Time
	pendingReload    int // 连续失败计数
	notBefore        time.Time
	notAfter         time.Time
	dnsNames         []string
	issuer           string
	subject          string
	serialNumber     string
	sigAlg           string

	// Start 生命周期
	ctx    context.Context
	cancel context.CancelFunc
	startOnce sync.Once
	closeOnce sync.Once

	log *slog.Logger
}

// NewCertReloader 创建 CertReloader 并立即执行一次 LoadNow。
// 若首次加载失败则返回 error。
func NewCertReloader(certPath, keyPath string, log *slog.Logger) (*CertReloader, error) {
	if keyPath == "" || certPath == "" {
		return nil, errTLSPathsRequired
	}
	c := &CertReloader{
		certPath: certPath,
		keyPath:  keyPath,
		log:      log,
	}
	// 首次加载：用 ValidateTLSPaths 的默认行为（不校验 owner——Manager 已在校验入口处处理）
	if err := c.LoadNow(); err != nil {
		return nil, err
	}
	// 如果首次 LoadNow 成功但 atomic 值为空（理论上不会），兜底
	if c.cert.Load() == nil {
		return nil, errTLSInitialCertEmpty
	}
	return c, nil
}

var (
	errTLSPathsRequired    = errCode{"config_missing", "cert path and key path are required"}
	errTLSInitialCertEmpty = errCode{"cert_empty", "initial certificate load produced empty value"}
)

type errCode struct{ code, msg string }

func (e errCode) Error() string { return e.code + ": " + e.msg }

// Start 启动后台 ticker goroutine（10s 检查）。可重复调用——幂等。
// ctx 用于在进程关闭时取消。
func (c *CertReloader) Start(ctx context.Context) {
	c.startOnce.Do(func() {
		c.ctx, c.cancel = context.WithCancel(ctx)
		go c.loop()
	})
}

// Close 停止后台 ticker。幂等。
func (c *CertReloader) Close() {
	c.closeOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
	})
}

func (c *CertReloader) loop() {
	ticker := time.NewTicker(reloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			_ = c.LoadNow()
		}
	}
}

// LoadNow 同步执行一次证书重新加载。
// 成功返回 nil；失败保留 last known good certificate。
func (c *CertReloader) LoadNow() error {
	cleanCert, cleanKey, leaf, err := ValidateTLSPaths(c.certPath, c.keyPath, false)
	// ownerUid 检查由上层调用方在 ValidateTLSPaths 的独立调用或 NewWithEndpoint 中处理；
	// 这里 CertReloader 只关心文件能否被解析，不重复做权限/属主强校验。
	if err != nil {
		return c.recordFailure("validate_failed", err)
	}
	_ = cleanCert
	_ = cleanKey

	pair, err := tls.LoadX509KeyPair(cleanCert, cleanKey)
	if err != nil {
		// G6 部分写入保护：连续 3 次失败才真正上报为 last error
		c.mu.Lock()
		c.pendingReload++
		count := c.pendingReload
		c.mu.Unlock()
		if count < 3 {
			c.log.Warn("tls_reload_pending",
				slog.Int("attempt", count),
				slog.String("error", truncateErr(err, 180)),
			)
			// 不更新 lastErr，不覆盖证书
			return err
		}
		return c.recordFailure("load_key_pair_failed", err)
	}

	// 提取 leaf 证书元信息
	if leaf == nil && len(pair.Certificate) > 0 {
		if parsed, perr := x509.ParseCertificate(pair.Certificate[0]); perr == nil {
			leaf = parsed
		}
	}

	// 提交新证书
	c.cert.Store(&pair)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingReload = 0
	c.lastErr = nil
	if leaf != nil {
		c.notBefore = leaf.NotBefore
		c.notAfter = leaf.NotAfter
		c.dnsNames = append([]string(nil), leaf.DNSNames...)
		c.issuer = leaf.Issuer.String()
		c.subject = leaf.Subject.String()
		c.serialNumber = leaf.SerialNumber.String()
		c.sigAlg = leaf.SignatureAlgorithm.String()
	}
	return nil
}

// recordFailure 统一记录失败：限频 60s，不覆盖 atomic 中的好证书。
func (c *CertReloader) recordFailure(tag string, err error) error {
	c.mu.Lock()
	c.pendingReload = 0
	c.lastErr = errCode{code: tag, msg: err.Error()}
	now := time.Now()
	shouldLog := now.Sub(c.lastErrLoggedAt) > 60*time.Second
	if shouldLog {
		c.lastErrLoggedAt = now
	}
	c.mu.Unlock()

	if shouldLog {
		c.log.Warn("tls_reload_failed",
			slog.String("code", tag),
			slog.String("error", truncateErr(err, 200)),
		)
	}
	return err
}

// GetCertificate 是 tls.Config.GetCertificate 回调。
// 严格遵守 M4：零 I/O，零锁，只做 atomic.Load。
func (c *CertReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	v := c.cert.Load()
	if v == nil {
		return nil, errCode{"no_cert_loaded", "no tls certificate has been loaded yet"}
	}
	return v.(*tls.Certificate), nil
}

// Snapshot 返回当前证书的只读元信息。
// 用于 Manager.Endpoint() 序列化给前端。
func (c *CertReloader) Snapshot() TLSStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := TLSStatus{
		DNSNames:     append([]string(nil), c.dnsNames...),
		NotBefore:    c.notBefore,
		NotAfter:     c.notAfter,
		Issuer:       c.issuer,
		Subject:      c.subject,
		SerialNumber: c.serialNumber,
		SigAlg:       c.sigAlg,
	}
	if c.lastErr != nil {
		s.LastError = c.lastErr.Error()
	}
	return s
}

// truncateErr 用于日志脱敏与长度限制（安全日志）。
func truncateErr(err error, n int) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
