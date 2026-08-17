package codexgateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRunLocalChatDoesNotStartAppServerForCanceledRequest(t *testing.T) {
	svc := NewService(nil, nil).WithLocalCodex(t.TempDir(), "binary-that-must-not-run", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for i := 0; i < 20; i++ {
		_, err := svc.RunLocalChat(ctx, ChatCompletionRequest{}, "")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run %d error = %v, want context canceled before process start", i, err)
		}
	}
}

func TestBuildLocalCodexIsolationConfig(t *testing.T) {
	configRaw := json.RawMessage(`{"config":{"mcp_servers":{"docs":{},"private":{}},"sandbox_mode":null,"sandbox_workspace_write":null}}`)
	skillsRaw := json.RawMessage(`{"data":[{"cwd":"/gateway/work","skills":[{"path":"/skills/z/SKILL.md"},{"path":"/skills/a/SKILL.md"},{"path":"/skills/z/SKILL.md"}],"errors":[]}]}`)

	config, err := buildLocalCodexIsolationConfig(configRaw, skillsRaw, "/gateway/work")
	if err != nil {
		t.Fatalf("build isolation config: %v", err)
	}
	features := config["features"].(map[string]any)
	for _, key := range []string{"apps", "hooks", "multi_agent", "remote_plugin"} {
		if enabled, ok := features[key].(bool); !ok || enabled {
			t.Fatalf("expected feature %q disabled, got %#v", key, features[key])
		}
	}
	mcpServers := config["mcp_servers"].(map[string]any)
	if len(mcpServers) != 2 {
		t.Fatalf("expected both MCP servers disabled, got %#v", mcpServers)
	}
	for name, raw := range mcpServers {
		if raw.(map[string]any)["enabled"] != false {
			t.Fatalf("expected MCP server %q disabled, got %#v", name, raw)
		}
	}
	skillConfig := config["skills"].(map[string]any)["config"].([]map[string]any)
	if len(skillConfig) != 2 || skillConfig[0]["path"] != "/skills/a/SKILL.md" || skillConfig[1]["path"] != "/skills/z/SKILL.md" {
		t.Fatalf("expected unique sorted disabled skills, got %#v", skillConfig)
	}
	for _, skill := range skillConfig {
		if skill["enabled"] != false {
			t.Fatalf("expected skill disabled, got %#v", skill)
		}
	}
	if config["default_permissions"] != localCodexPermissionProfile {
		t.Fatalf("expected Gateway permission profile, got %#v", config["default_permissions"])
	}
	profiles := config["permissions"].(map[string]any)
	profile := profiles[localCodexPermissionProfile].(map[string]any)
	filesystem := profile["filesystem"].(map[string]any)
	if filesystem[":root"] != "deny" || filesystem["/gateway/work"] != "read" {
		t.Fatalf("expected root-deny/workdir-read filesystem policy, got %#v", filesystem)
	}
	if profile["network"].(map[string]any)["enabled"] != false {
		t.Fatalf("expected network disabled, got %#v", profile["network"])
	}
}

func TestBuildLocalCodexIsolationConfigRejectsUnsafeInputs(t *testing.T) {
	tests := []struct {
		name       string
		configRaw  string
		skillsRaw  string
		workDir    string
		wantSubstr string
	}{
		{
			name:       "relative workdir",
			configRaw:  `{"config":{}}`,
			skillsRaw:  `{"data":[{"cwd":"relative","skills":[],"errors":[]}]}`,
			workDir:    "relative",
			wantSubstr: "must be absolute",
		},
		{
			name:       "legacy sandbox",
			configRaw:  `{"config":{"sandbox_mode":"read-only"}}`,
			skillsRaw:  `{"data":[{"cwd":"/gateway/work","skills":[],"errors":[]}]}`,
			workDir:    "/gateway/work",
			wantSubstr: "legacy Codex sandbox",
		},
		{
			name:       "skill discovery error",
			configRaw:  `{"config":{}}`,
			skillsRaw:  `{"data":[{"cwd":"/gateway/work","skills":[],"errors":[{"message":"broken"}]}]}`,
			workDir:    "/gateway/work",
			wantSubstr: "skill discovery errors",
		},
		{
			name:       "skill without path",
			configRaw:  `{"config":{}}`,
			skillsRaw:  `{"data":[{"cwd":"/gateway/work","skills":[{"path":""}],"errors":[]}]}`,
			workDir:    "/gateway/work",
			wantSubstr: "without a path",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildLocalCodexIsolationConfig(json.RawMessage(tt.configRaw), json.RawMessage(tt.skillsRaw), tt.workDir)
			if err == nil || !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantSubstr, err)
			}
		})
	}
}

func TestVerifyLocalCodexThreadIsolation(t *testing.T) {
	valid := json.RawMessage(`{"activePermissionProfile":{"id":"phantom-gateway-api"},"instructionSources":[],"sandbox":{"type":"readOnly","networkAccess":false}}`)
	if err := verifyLocalCodexThreadIsolation(valid); err != nil {
		t.Fatalf("verify valid isolation: %v", err)
	}

	invalid := []json.RawMessage{
		json.RawMessage(`{"activePermissionProfile":null,"instructionSources":[],"sandbox":{"type":"readOnly","networkAccess":false}}`),
		json.RawMessage(`{"activePermissionProfile":{"id":"phantom-gateway-api"},"instructionSources":["/workspace/AGENTS.md"],"sandbox":{"type":"readOnly","networkAccess":false}}`),
		json.RawMessage(`{"activePermissionProfile":{"id":"phantom-gateway-api"},"instructionSources":[],"sandbox":{"type":"readOnly","networkAccess":true}}`),
	}
	for _, raw := range invalid {
		if err := verifyLocalCodexThreadIsolation(raw); err == nil {
			t.Fatalf("expected invalid isolation state to fail: %s", raw)
		}
	}
}

func TestLocalCodexCapabilitySetEmpty(t *testing.T) {
	tests := []struct {
		raw       string
		wantEmpty bool
		wantError bool
	}{
		{raw: `null`, wantEmpty: true},
		{raw: `[]`, wantEmpty: true},
		{raw: `{}`, wantEmpty: true},
		{raw: `[{"name":"search"}]`},
		{raw: `{"search":{"enabled":true}}`},
		{raw: `"unexpected"`, wantError: true},
	}
	for _, tt := range tests {
		empty, err := localCodexCapabilitySetEmpty(json.RawMessage(tt.raw))
		if (err != nil) != tt.wantError {
			t.Fatalf("raw %s: expected error=%v, got %v", tt.raw, tt.wantError, err)
		}
		if !tt.wantError && empty != tt.wantEmpty {
			t.Fatalf("raw %s: expected empty=%v, got %v", tt.raw, tt.wantEmpty, empty)
		}
	}
}
