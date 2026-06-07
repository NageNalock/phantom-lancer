package v2ray

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	core "github.com/v2fly/v2ray-core/v5"
	"github.com/v2fly/v2ray-core/v5/app/dispatcher"
	v2log "github.com/v2fly/v2ray-core/v5/app/log"
	"github.com/v2fly/v2ray-core/v5/app/proxyman"
	_ "github.com/v2fly/v2ray-core/v5/app/proxyman/inbound"
	_ "github.com/v2fly/v2ray-core/v5/app/proxyman/outbound"
	"github.com/v2fly/v2ray-core/v5/app/router"
	routercommon "github.com/v2fly/v2ray-core/v5/app/router/routercommon"
	v2logcommon "github.com/v2fly/v2ray-core/v5/common/log"
	v2net "github.com/v2fly/v2ray-core/v5/common/net"
	"github.com/v2fly/v2ray-core/v5/common/protocol"
	"github.com/v2fly/v2ray-core/v5/common/serial"
	"github.com/v2fly/v2ray-core/v5/proxy/blackhole"
	"github.com/v2fly/v2ray-core/v5/proxy/freedom"
	vmess "github.com/v2fly/v2ray-core/v5/proxy/vmess"
	vmessinbound "github.com/v2fly/v2ray-core/v5/proxy/vmess/inbound"
	"github.com/v2fly/v2ray-core/v5/transport/internet"
	_ "github.com/v2fly/v2ray-core/v5/transport/internet/tcp"
	"github.com/v2fly/v2ray-core/v5/transport/internet/tls"
	"github.com/v2fly/v2ray-core/v5/transport/internet/websocket"
	"google.golang.org/protobuf/types/known/anypb"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/storage"
)

const (
	scope          = "v2ray_service"
	scopeID        = "default"
	configFileName = "config.generated.json"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Service struct {
	Store   *storage.Store
	Hub     *events.Hub
	DataDir string
	Log     *slog.Logger

	mu          sync.Mutex
	instance    *core.Instance
	startedAt   time.Time
	configHash  string
	versionID   string
	lastError   string
	lastEventAt string
}

type Status struct {
	Available            bool   `json:"available"`
	CoreVersion          string `json:"coreVersion"`
	State                string `json:"state"`
	Running              bool   `json:"running"`
	Enabled              bool   `json:"enabled"`
	StartOnPhantomLaunch bool   `json:"startOnPhantomLaunch"`
	Endpoint             string `json:"endpoint"`
	Listen               string `json:"listen"`
	Port                 int    `json:"port"`
	Protocol             string `json:"protocol"`
	Transport            string `json:"transport"`
	ConfigMode           string `json:"configMode"`
	ConfigHash           string `json:"configHash,omitempty"`
	ConfigVersionID      string `json:"configVersionId,omitempty"`
	StartedAt            string `json:"startedAt,omitempty"`
	UptimeSeconds        int64  `json:"uptimeSeconds"`
	LastError            string `json:"lastError,omitempty"`
	LastEventAt          string `json:"lastEventAt,omitempty"`
	RemoteClientCount    int    `json:"remoteClientCount"`
	EnabledRemoteClients int    `json:"enabledRemoteClients"`
	ConfigPath           string `json:"configPath"`
	Stale                bool   `json:"stale"`
}

type ValidationResult struct {
	OK           bool   `json:"ok"`
	Message      string `json:"message"`
	ConfigHash   string `json:"configHash,omitempty"`
	SettingsHash string `json:"settingsHash,omitempty"`
	ConfigJSON   string `json:"configJson,omitempty"`
}

type ExportedClient struct {
	ClientID      string         `json:"clientId"`
	Label         string         `json:"label"`
	Endpoint      string         `json:"endpoint"`
	ShareURI      string         `json:"shareUri"`
	ClientConfig  map[string]any `json:"clientConfig"`
	ServerSummary map[string]any `json:"serverSummary"`
}

func NewService(store *storage.Store, hub *events.Hub, dataDir string, logger *slog.Logger) *Service {
	return &Service{
		Store:   store,
		Hub:     hub,
		DataDir: filepath.Join(dataDir, "v2ray"),
		Log:     logger,
	}
}

func (s *Service) Ensure(ctx context.Context) error {
	return s.Store.EnsureV2RaySettings(ctx)
}

func (s *Service) Status(ctx context.Context) Status {
	settings, err := s.Store.GetV2RaySettings(ctx)
	if err != nil {
		return Status{Available: true, CoreVersion: core.Version(), State: "error", LastError: err.Error()}
	}
	clients, _ := s.Store.ListV2RayRemoteClients(ctx, false)
	enabledClients := 0
	for _, client := range clients {
		if client.Enabled && client.RevokedAt == "" {
			enabledClients++
		}
	}
	var latestConfigHash string
	if _, _, generatedHash, _, err := s.prepareConfig(settings, clients); err == nil {
		latestConfigHash = generatedHash
	}

	s.mu.Lock()
	running := s.instance != nil
	startedAt := s.startedAt
	configHash := s.configHash
	versionID := s.versionID
	lastError := s.lastError
	lastEventAt := s.lastEventAt
	s.mu.Unlock()

	state := "stopped"
	if running {
		state = "running"
	}
	if lastError != "" && !running {
		state = "failed"
	}

	var started string
	var uptime int64
	if !startedAt.IsZero() {
		started = startedAt.UTC().Format(time.RFC3339Nano)
		if running {
			uptime = int64(time.Since(startedAt).Seconds())
		}
	}

	return Status{
		Available:            true,
		CoreVersion:          core.Version(),
		State:                state,
		Running:              running,
		Enabled:              settings.Enabled,
		StartOnPhantomLaunch: settings.StartOnPhantomLaunch,
		Endpoint:             endpoint(settings),
		Listen:               settings.Listen,
		Port:                 settings.Port,
		Protocol:             settings.Protocol,
		Transport:            settings.Transport,
		ConfigMode:           settings.ConfigMode,
		ConfigHash:           configHash,
		ConfigVersionID:      versionID,
		StartedAt:            started,
		UptimeSeconds:        uptime,
		LastError:            lastError,
		LastEventAt:          lastEventAt,
		RemoteClientCount:    len(clients),
		EnabledRemoteClients: enabledClients,
		ConfigPath:           s.configPath(),
		Stale:                running && configHash != "" && latestConfigHash != "" && configHash != latestConfigHash,
	}
}

func (s *Service) Validate(ctx context.Context, settings storage.V2RaySettings, clients []storage.V2RayRemoteClient) (ValidationResult, error) {
	configJSON, settingsHash, configHash, config, err := s.prepareConfig(settings, clients)
	if err != nil {
		return ValidationResult{OK: false, Message: err.Error()}, err
	}
	instance, err := core.New(config)
	if err != nil {
		return ValidationResult{OK: false, Message: err.Error(), ConfigHash: configHash, SettingsHash: settingsHash, ConfigJSON: string(configJSON)}, err
	}
	_ = instance.Close()
	return ValidationResult{OK: true, Message: "配置校验通过", ConfigHash: configHash, SettingsHash: settingsHash, ConfigJSON: string(configJSON)}, nil
}

func (s *Service) Start(ctx context.Context) (Status, error) {
	s.mu.Lock()
	if s.instance != nil {
		s.mu.Unlock()
		return s.Status(ctx), nil
	}
	s.mu.Unlock()

	settings, err := s.Store.GetV2RaySettings(ctx)
	if err != nil {
		return s.Status(ctx), err
	}
	clients, err := s.Store.ListV2RayRemoteClients(ctx, false)
	if err != nil {
		return s.Status(ctx), err
	}
	configJSON, settingsHash, configHash, config, err := s.prepareConfig(settings, clients)
	if err != nil {
		s.setError(err.Error())
		return s.Status(ctx), err
	}
	if err := checkPortAvailable(settings.Listen, settings.Port); err != nil {
		s.setError(err.Error())
		return s.Status(ctx), err
	}
	if err := os.MkdirAll(s.DataDir, 0o700); err != nil {
		s.setError(err.Error())
		return s.Status(ctx), err
	}
	if err := os.WriteFile(s.configPath(), configJSON, 0o600); err != nil {
		s.setError(err.Error())
		return s.Status(ctx), err
	}
	version, _ := s.Store.AddV2RayConfigVersion(ctx, storage.V2RayConfigVersion{
		SettingsHash:     settingsHash,
		ConfigHash:       configHash,
		ConfigJSONRedact: string(redactConfig(configJSON)),
		ValidationStatus: "valid",
		ValidationOutput: "embedded core validation passed",
	})

	instance, err := core.New(config)
	if err != nil {
		s.setError(err.Error())
		return s.Status(ctx), err
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		s.setError(err.Error())
		return s.Status(ctx), err
	}
	if version.ID != "" {
		_ = s.Store.MarkV2RayConfigActivated(ctx, version.ID)
	}
	_, _ = s.Store.UpdateV2RaySettings(ctx, withEnabled(settings, true))

	s.mu.Lock()
	s.instance = instance
	s.startedAt = time.Now()
	s.configHash = configHash
	s.versionID = version.ID
	s.lastError = ""
	s.mu.Unlock()
	s.append(ctx, "v2ray.started", map[string]any{"endpoint": endpoint(settings), "configHash": configHash})
	return s.Status(ctx), nil
}

func (s *Service) Stop(ctx context.Context) (Status, error) {
	s.mu.Lock()
	instance := s.instance
	s.instance = nil
	s.startedAt = time.Time{}
	s.mu.Unlock()

	if instance != nil {
		if err := instance.Close(); err != nil {
			s.setError(err.Error())
			return s.Status(ctx), err
		}
	}
	settings, err := s.Store.GetV2RaySettings(ctx)
	if err == nil {
		_, _ = s.Store.UpdateV2RaySettings(ctx, withEnabled(settings, false))
	}
	s.append(ctx, "v2ray.stopped", map[string]any{"reason": "requested"})
	return s.Status(ctx), nil
}

func (s *Service) Restart(ctx context.Context) (Status, error) {
	if _, err := s.Stop(ctx); err != nil {
		return s.Status(ctx), err
	}
	status, err := s.Start(ctx)
	if err != nil {
		return status, err
	}
	s.append(ctx, "v2ray.restarted", map[string]any{"endpoint": status.Endpoint, "configHash": status.ConfigHash})
	return status, nil
}

func (s *Service) Close() {
	s.mu.Lock()
	instance := s.instance
	s.instance = nil
	s.mu.Unlock()
	if instance != nil {
		_ = instance.Close()
	}
}

func (s *Service) ExportClient(ctx context.Context, client storage.V2RayRemoteClient) (ExportedClient, error) {
	settings, err := s.Store.GetV2RaySettings(ctx)
	if err != nil {
		return ExportedClient{}, err
	}
	host := settings.PublicHost
	if strings.TrimSpace(host) == "" {
		host = settings.Listen
	}
	if host == "0.0.0.0" || host == "::" {
		host = "<server-public-host>"
	}
	outbound := map[string]any{
		"protocol": "vmess",
		"settings": map[string]any{
			"vnext": []map[string]any{{
				"address": host,
				"port":    settings.Port,
				"users": []map[string]any{{
					"id":       client.UUID,
					"alterId":  client.AlterID,
					"security": "auto",
					"level":    client.Level,
				}},
			}},
		},
		"streamSettings": streamSettings(settings),
	}
	clientConfig := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []map[string]any{{
			"listen":   "127.0.0.1",
			"port":     10808,
			"protocol": "socks",
			"settings": map[string]any{"udp": true},
		}},
		"outbounds": []map[string]any{outbound},
	}
	share := vmessShare(settings, client, host)
	return ExportedClient{
		ClientID:     client.ID,
		Label:        client.Label,
		Endpoint:     host + ":" + strconv.Itoa(settings.Port),
		ShareURI:     share,
		ClientConfig: clientConfig,
		ServerSummary: map[string]any{
			"protocol":  settings.Protocol,
			"transport": settings.Transport,
			"security":  settings.Security,
			"host":      host,
			"port":      settings.Port,
		},
	}, nil
}

func (s *Service) prepareConfig(settings storage.V2RaySettings, clients []storage.V2RayRemoteClient) ([]byte, string, string, *core.Config, error) {
	settings = storage.NormalizeV2RaySettings(settings)
	if err := validateSettings(settings); err != nil {
		return nil, "", "", nil, err
	}
	enabled := make([]storage.V2RayRemoteClient, 0, len(clients))
	for _, client := range clients {
		if client.Enabled && client.RevokedAt == "" {
			if !uuidPattern.MatchString(client.UUID) {
				return nil, "", "", nil, fmt.Errorf("远程设备 %s 的 UUID 无效", client.Label)
			}
			enabled = append(enabled, client)
		}
	}
	if settings.ConfigMode == "raw_json" {
		return nil, "", "", nil, errors.New("内嵌 V2Ray MVP 仅支持受控表单配置，暂不支持 raw JSON")
	}
	if len(enabled) == 0 {
		return nil, "", "", nil, errors.New("至少需要一个启用的远程设备 UUID")
	}
	config := serverConfig(settings, enabled)
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, "", "", nil, err
	}
	coreConfig, err := s.coreConfig(settings, enabled)
	if err != nil {
		return nil, "", "", nil, err
	}
	return data, hashSettings(settings, clients), hashBytes(data), coreConfig, nil
}

func validateSettings(settings storage.V2RaySettings) error {
	if settings.Port < 1 || settings.Port > 65535 {
		return errors.New("V2Ray 端口必须在 1-65535 之间")
	}
	if settings.Protocol != "vmess" {
		return errors.New("MVP 仅支持 VMess")
	}
	if settings.Transport != "tcp" && settings.Transport != "ws" {
		return errors.New("MVP 仅支持 tcp 或 ws 传输")
	}
	if settings.Transport == "ws" && settings.WSPath == "" {
		return errors.New("WebSocket 传输需要配置 path")
	}
	if settings.Security != "none" && settings.Security != "tls" {
		return errors.New("MVP 仅支持 none 或 tls")
	}
	if settings.Security == "tls" && (settings.TLSCertFile == "" || settings.TLSKeyFile == "") {
		return errors.New("TLS 需要证书和私钥路径")
	}
	if settings.ConfigMode != "guided" && settings.ConfigMode != "raw_json" {
		return errors.New("配置模式必须是 guided 或 raw_json")
	}
	return nil
}

func serverConfig(settings storage.V2RaySettings, clients []storage.V2RayRemoteClient) map[string]any {
	vclients := make([]map[string]any, 0, len(clients))
	for _, client := range clients {
		vclients = append(vclients, map[string]any{
			"id":      client.UUID,
			"level":   client.Level,
			"alterId": client.AlterID,
			"email":   client.Email,
		})
	}
	rules := []map[string]any{}
	if settings.BlockPrivateNetwork {
		rules = append(rules, map[string]any{
			"type": "field",
			"ip": []string{
				"10.0.0.0/8",
				"172.16.0.0/12",
				"192.168.0.0/16",
				"127.0.0.0/8",
				"169.254.0.0/16",
				"fc00::/7",
				"fe80::/10",
				"::1/128",
			},
			"outboundTag": "blocked",
		})
	}
	inbound := map[string]any{
		"listen":   settings.Listen,
		"port":     settings.Port,
		"protocol": "vmess",
		"tag":      "pl-v2ray-in",
		"settings": map[string]any{
			"clients":                   vclients,
			"disableInsecureEncryption": true,
		},
		"streamSettings": streamSettings(settings),
	}
	if settings.SniffingEnabled {
		inbound["sniffing"] = map[string]any{
			"enabled":      true,
			"destOverride": []string{"http", "tls"},
		}
	}
	return map[string]any{
		"log": map[string]any{
			"access":   "",
			"error":    "",
			"loglevel": settings.LogLevel,
		},
		"inbounds": []map[string]any{inbound},
		"outbounds": []map[string]any{
			{"protocol": "freedom", "tag": "direct"},
			{"protocol": "blackhole", "tag": "blocked"},
		},
		"routing": map[string]any{
			"domainStrategy": "AsIs",
			"rules":          rules,
		},
	}
}

func (s *Service) coreConfig(settings storage.V2RaySettings, clients []storage.V2RayRemoteClient) (*core.Config, error) {
	stream, err := s.coreStreamSettings(settings)
	if err != nil {
		return nil, err
	}
	users := make([]*protocol.User, 0, len(clients))
	for _, client := range clients {
		users = append(users, &protocol.User{
			Level: uint32(client.Level),
			Email: client.Email,
			Account: serial.ToTypedMessage(&vmess.Account{
				Id:      client.UUID,
				AlterId: uint32(client.AlterID),
			}),
		})
	}

	receiver := &proxyman.ReceiverConfig{
		PortRange:      v2net.SinglePortRange(v2net.Port(settings.Port)),
		Listen:         v2net.NewIPOrDomain(v2net.ParseAddress(settings.Listen)),
		StreamSettings: stream,
	}
	if settings.SniffingEnabled {
		receiver.SniffingSettings = &proxyman.SniffingConfig{
			Enabled:             true,
			DestinationOverride: []string{"http", "tls"},
		}
	}

	apps := []*anypb.Any{
		serial.ToTypedMessage(&v2log.Config{
			Error: &v2log.LogSpecification{
				Type:  v2log.LogType_Console,
				Level: logSeverity(settings.LogLevel),
			},
			Access: &v2log.LogSpecification{
				Type: v2log.LogType_None,
			},
		}),
		serial.ToTypedMessage(&dispatcher.Config{}),
		serial.ToTypedMessage(&proxyman.InboundConfig{}),
		serial.ToTypedMessage(&proxyman.OutboundConfig{}),
	}
	if settings.BlockPrivateNetwork {
		apps = append(apps, serial.ToTypedMessage(&router.Config{
			DomainStrategy: router.DomainStrategy_IpOnDemand,
			Rule: []*router.RoutingRule{
				{
					TargetTag: &router.RoutingRule_Tag{Tag: "blocked"},
					Cidr:      privateCIDRs(),
				},
			},
		}))
	}

	return &core.Config{
		App: apps,
		Inbound: []*core.InboundHandlerConfig{
			{
				Tag:              "pl-v2ray-in",
				ReceiverSettings: serial.ToTypedMessage(receiver),
				ProxySettings: serial.ToTypedMessage(&vmessinbound.Config{
					User:                 users,
					SecureEncryptionOnly: true,
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				Tag:           "direct",
				ProxySettings: serial.ToTypedMessage(&freedom.Config{}),
			},
			{
				Tag: "blocked",
				ProxySettings: serial.ToTypedMessage(&blackhole.Config{
					Response: serial.ToTypedMessage(&blackhole.NoneResponse{}),
				}),
			},
		},
	}, nil
}

func (s *Service) coreStreamSettings(settings storage.V2RaySettings) (*internet.StreamConfig, error) {
	protocolName := "tcp"
	if settings.Transport == "ws" {
		protocolName = "websocket"
	}
	stream := &internet.StreamConfig{ProtocolName: protocolName}
	if settings.Transport == "ws" {
		stream.TransportSettings = []*internet.TransportConfig{
			{
				ProtocolName: "websocket",
				Settings: serial.ToTypedMessage(&websocket.Config{
					Path: settings.WSPath,
				}),
			},
		}
	}
	if settings.Security == "tls" {
		certPEM, err := os.ReadFile(settings.TLSCertFile)
		if err != nil {
			return nil, fmt.Errorf("读取 TLS 证书失败: %w", err)
		}
		keyPEM, err := os.ReadFile(settings.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("读取 TLS 私钥失败: %w", err)
		}
		tlsConfig := &tls.Config{
			Certificate: []*tls.Certificate{
				{
					Certificate: certPEM,
					Key:         keyPEM,
				},
			},
		}
		stream.SecurityType = serial.GetMessageType(tlsConfig)
		stream.SecuritySettings = []*anypb.Any{serial.ToTypedMessage(tlsConfig)}
	}
	return stream, nil
}

func streamSettings(settings storage.V2RaySettings) map[string]any {
	stream := map[string]any{
		"network":  settings.Transport,
		"security": settings.Security,
	}
	if settings.Transport == "ws" {
		stream["wsSettings"] = map[string]any{"path": settings.WSPath}
	}
	if settings.Security == "tls" {
		stream["tlsSettings"] = map[string]any{
			"certificates": []map[string]any{{
				"certificateFile": settings.TLSCertFile,
				"keyFile":         settings.TLSKeyFile,
			}},
		}
	}
	return stream
}

func vmessShare(settings storage.V2RaySettings, client storage.V2RayRemoteClient, host string) string {
	payload := map[string]string{
		"v":    "2",
		"ps":   client.Label,
		"add":  host,
		"port": strconv.Itoa(settings.Port),
		"id":   client.UUID,
		"aid":  strconv.Itoa(client.AlterID),
		"net":  settings.Transport,
		"type": "none",
		"host": "",
		"path": settings.WSPath,
		"tls":  settings.Security,
	}
	data, _ := json.Marshal(payload)
	return "vmess://" + base64.StdEncoding.EncodeToString(data)
}

func logSeverity(level string) v2logcommon.Severity {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return v2logcommon.Severity_Debug
	case "info":
		return v2logcommon.Severity_Info
	case "error":
		return v2logcommon.Severity_Error
	default:
		return v2logcommon.Severity_Warning
	}
}

func privateCIDRs() []*routercommon.CIDR {
	ranges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"fc00::/7",
		"fe80::/10",
		"::1/128",
	}
	cidrs := make([]*routercommon.CIDR, 0, len(ranges))
	for _, item := range ranges {
		ip, network, err := net.ParseCIDR(item)
		if err != nil {
			continue
		}
		ones, _ := network.Mask.Size()
		raw := ip.To4()
		if raw == nil {
			raw = ip.To16()
		}
		if raw == nil {
			continue
		}
		cidrs = append(cidrs, &routercommon.CIDR{
			Ip:     raw,
			Prefix: uint32(ones),
		})
	}
	return cidrs
}

func withEnabled(settings storage.V2RaySettings, enabled bool) storage.V2RaySettings {
	settings.Enabled = enabled
	return settings
}

func checkPortAvailable(listen string, port int) error {
	host := listen
	if host == "" || host == "0.0.0.0" {
		host = ""
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("端口不可用: %w", err)
	}
	return ln.Close()
}

func endpoint(settings storage.V2RaySettings) string {
	host := settings.PublicHost
	if host == "" {
		host = settings.Listen
	}
	return host + ":" + strconv.Itoa(settings.Port)
}

func hashSettings(settings storage.V2RaySettings, clients []storage.V2RayRemoteClient) string {
	payload := map[string]any{"settings": settings, "clients": clients}
	data, _ := json.Marshal(payload)
	return hashBytes(data)
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func redactConfig(data []byte) []byte {
	var config any
	if err := json.Unmarshal(data, &config); err != nil {
		return []byte("{}")
	}
	redactValue(config)
	out, _ := json.MarshalIndent(config, "", "  ")
	return out
}

func redactValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(key)
			if lower == "id" || strings.Contains(lower, "password") || strings.Contains(lower, "token") {
				if text, ok := child.(string); ok {
					typed[key] = mask(text)
				}
				continue
			}
			redactValue(child)
		}
	case []any:
		for _, child := range typed {
			redactValue(child)
		}
	}
}

func mask(value string) string {
	if len(value) <= 8 {
		return "****"
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func GenerateUUID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4],
		buf[4:6],
		buf[6:8],
		buf[8:10],
		buf[10:16],
	), nil
}

func (s *Service) configPath() string {
	return filepath.Join(s.DataDir, configFileName)
}

func (s *Service) setError(message string) {
	s.mu.Lock()
	s.lastError = message
	s.mu.Unlock()
}

func (s *Service) append(ctx context.Context, eventType string, payload map[string]any) {
	event, err := s.Store.AppendEvent(ctx, scope, scopeID, eventType, payload)
	if err == nil {
		s.Hub.Publish(event)
		s.mu.Lock()
		s.lastEventAt = event.CreatedAt
		s.mu.Unlock()
	}
	if s.Log != nil {
		s.Log.Info("v2ray event", "type", eventType)
	}
}
