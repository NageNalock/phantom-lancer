package codexclient

import (
	"context"
	"strconv"
	"strings"

	"phantom-lancer/internal/storage"
)

// Settings keys use the codex_cli.* prefix to avoid colliding with the retired
// codex.* settings of the previous implementation.
const settingsPrefix = "codex_cli."

const (
	keyEnabled              = settingsPrefix + "enabled"
	keyBinaryPath           = settingsPrefix + "binary_path"
	keyCodexHome            = settingsPrefix + "codex_home"
	keyDefaultModel         = settingsPrefix + "default_model"
	keyDefaultSandbox       = settingsPrefix + "default_sandbox"
	keyDefaultApproval      = settingsPrefix + "default_approval_policy"
	keyAppServerEnabled     = settingsPrefix + "app_server_enabled"
	keyAppServerProbeSecs   = settingsPrefix + "app_server_probe_interval_seconds"
	keyAppServerStartLaunch = settingsPrefix + "app_server_start_on_launch"
	keyExecFallbackEnabled  = settingsPrefix + "exec_fallback_enabled"
	keyEventRetentionDays   = settingsPrefix + "event_retention_days"
	keyMaxEventsPerThread   = settingsPrefix + "max_events_per_thread"
	keyMaxEventPayloadBytes = settingsPrefix + "max_event_payload_bytes"
	keyMaxConcurrentTurns   = settingsPrefix + "max_concurrent_turns"
)

// Settings is the persisted module configuration for the Codex CLI client.
type Settings struct {
	Enabled                bool   `json:"enabled"`
	BinaryPath             string `json:"binaryPath"`
	CodexHome              string `json:"codexHome"`
	DefaultModel           string `json:"defaultModel"`
	DefaultSandbox         string `json:"defaultSandbox"`
	DefaultApprovalPolicy  string `json:"defaultApprovalPolicy"`
	AppServerEnabled       bool   `json:"appServerEnabled"`
	AppServerProbeSeconds  int    `json:"appServerProbeIntervalSeconds"`
	AppServerStartOnLaunch bool   `json:"appServerStartOnLaunch"`
	ExecFallbackEnabled    bool   `json:"execFallbackEnabled"`
	EventRetentionDays     int    `json:"eventRetentionDays"`
	MaxEventsPerThread     int    `json:"maxEventsPerThread"`
	MaxEventPayloadBytes   int    `json:"maxEventPayloadBytes"`
	MaxConcurrentTurns     int    `json:"maxConcurrentTurns"`
}

func DefaultSettings() Settings {
	return Settings{
		Enabled:                true,
		BinaryPath:             "",
		CodexHome:              "",
		DefaultModel:           "",
		DefaultSandbox:         "read-only",
		DefaultApprovalPolicy:  "on-request",
		AppServerEnabled:       true,
		AppServerProbeSeconds:  20,
		AppServerStartOnLaunch: false,
		ExecFallbackEnabled:    true,
		EventRetentionDays:     14,
		MaxEventsPerThread:     2000,
		MaxEventPayloadBytes:   64 * 1024,
		MaxConcurrentTurns:     1,
	}
}

func normalizeSettings(s Settings) Settings {
	defaults := DefaultSettings()
	s.BinaryPath = strings.TrimSpace(s.BinaryPath)
	s.CodexHome = strings.TrimSpace(s.CodexHome)
	s.DefaultModel = strings.TrimSpace(s.DefaultModel)
	s.DefaultSandbox = strings.TrimSpace(s.DefaultSandbox)
	switch s.DefaultSandbox {
	case "read-only", "workspace-write":
	default:
		s.DefaultSandbox = defaults.DefaultSandbox
	}
	s.DefaultApprovalPolicy = strings.TrimSpace(s.DefaultApprovalPolicy)
	if s.DefaultApprovalPolicy != "on-request" {
		s.DefaultApprovalPolicy = defaults.DefaultApprovalPolicy
	}
	if s.AppServerProbeSeconds < 5 {
		s.AppServerProbeSeconds = defaults.AppServerProbeSeconds
	}
	if s.AppServerProbeSeconds > 600 {
		s.AppServerProbeSeconds = 600
	}
	if s.EventRetentionDays < 0 {
		s.EventRetentionDays = defaults.EventRetentionDays
	}
	if s.MaxEventsPerThread <= 0 {
		s.MaxEventsPerThread = defaults.MaxEventsPerThread
	}
	if s.MaxEventPayloadBytes <= 0 {
		s.MaxEventPayloadBytes = defaults.MaxEventPayloadBytes
	}
	if s.MaxConcurrentTurns <= 0 {
		s.MaxConcurrentTurns = defaults.MaxConcurrentTurns
	}
	if s.MaxConcurrentTurns > 4 {
		s.MaxConcurrentTurns = 4
	}
	return s
}

func loadSettings(ctx context.Context, store *storage.Store) (Settings, error) {
	values, err := store.GetSettingsByPrefix(ctx, settingsPrefix)
	if err != nil {
		return Settings{}, err
	}
	s := DefaultSettings()
	if v, ok := values[keyEnabled]; ok {
		s.Enabled = parseBool(v)
	}
	if v, ok := values[keyBinaryPath]; ok {
		s.BinaryPath = v
	}
	if v, ok := values[keyCodexHome]; ok {
		s.CodexHome = v
	}
	if v, ok := values[keyDefaultModel]; ok {
		s.DefaultModel = v
	}
	if v, ok := values[keyDefaultSandbox]; ok {
		s.DefaultSandbox = v
	}
	if v, ok := values[keyDefaultApproval]; ok {
		s.DefaultApprovalPolicy = v
	}
	if v, ok := values[keyAppServerEnabled]; ok {
		s.AppServerEnabled = parseBool(v)
	}
	if v, ok := values[keyAppServerProbeSecs]; ok {
		s.AppServerProbeSeconds = parseInt(v, s.AppServerProbeSeconds)
	}
	if v, ok := values[keyAppServerStartLaunch]; ok {
		s.AppServerStartOnLaunch = parseBool(v)
	}
	if v, ok := values[keyExecFallbackEnabled]; ok {
		s.ExecFallbackEnabled = parseBool(v)
	}
	if v, ok := values[keyEventRetentionDays]; ok {
		s.EventRetentionDays = parseInt(v, s.EventRetentionDays)
	}
	if v, ok := values[keyMaxEventsPerThread]; ok {
		s.MaxEventsPerThread = parseInt(v, s.MaxEventsPerThread)
	}
	if v, ok := values[keyMaxEventPayloadBytes]; ok {
		s.MaxEventPayloadBytes = parseInt(v, s.MaxEventPayloadBytes)
	}
	if v, ok := values[keyMaxConcurrentTurns]; ok {
		s.MaxConcurrentTurns = parseInt(v, s.MaxConcurrentTurns)
	}
	return normalizeSettings(s), nil
}

func saveSettings(ctx context.Context, store *storage.Store, s Settings) error {
	s = normalizeSettings(s)
	return store.PutSettings(ctx, map[string]string{
		keyEnabled:              boolString(s.Enabled),
		keyBinaryPath:           s.BinaryPath,
		keyCodexHome:            s.CodexHome,
		keyDefaultModel:         s.DefaultModel,
		keyDefaultSandbox:       s.DefaultSandbox,
		keyDefaultApproval:      s.DefaultApprovalPolicy,
		keyAppServerEnabled:     boolString(s.AppServerEnabled),
		keyAppServerProbeSecs:   strconv.Itoa(s.AppServerProbeSeconds),
		keyAppServerStartLaunch: boolString(s.AppServerStartOnLaunch),
		keyExecFallbackEnabled:  boolString(s.ExecFallbackEnabled),
		keyEventRetentionDays:   strconv.Itoa(s.EventRetentionDays),
		keyMaxEventsPerThread:   strconv.Itoa(s.MaxEventsPerThread),
		keyMaxEventPayloadBytes: strconv.Itoa(s.MaxEventPayloadBytes),
		keyMaxConcurrentTurns:   strconv.Itoa(s.MaxConcurrentTurns),
	})
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func parseInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}
