package codexclient

import (
	"encoding/json"
	"strings"
)

// This file centralizes the v2 app-server (openai/codex) request shaping and
// response parsing so the wire formats live next to the protocol decisions made
// against the upstream source.

// appServerApprovalPolicy maps the internal approval policy to the AskForApproval
// string the app-server expects. The web console currently permits only
// on-request for owner-mediated runs; legacy or unexpected values fall back to
// on-request instead of disabling approvals.
func appServerApprovalPolicy(policy string) string {
	switch strings.TrimSpace(policy) {
	case "on-request", "onRequest":
		return "on-request"
	case "untrusted", "unless-trusted", "unlessTrusted":
		return "untrusted"
	default:
		return "on-request"
	}
}

// appServerSandboxMode maps the internal sandbox setting to the SandboxMode
// string used by thread/start (`sandbox`). Per the v2 schema the values are
// kebab-case: read-only/workspace-write. It never emits danger-full-access.
func appServerSandboxMode(sandbox string) string {
	switch strings.TrimSpace(sandbox) {
	case "workspace-write":
		return "workspace-write"
	default:
		return "read-only"
	}
}

// appServerSandboxPolicy builds the v2 sandboxPolicy object used by turn/start
// (`sandboxPolicy`). read-only maps to {type:"readOnly"}; workspace-write maps to
// {type:"workspaceWrite", writableRoots, networkAccess}. It never emits
// dangerFullAccess.
func appServerSandboxPolicy(policy RunPolicy, workspacePath string) map[string]any {
	switch policy.Sandbox {
	case "workspace-write":
		out := map[string]any{
			"type":          "workspaceWrite",
			"networkAccess": policy.NetworkEnabled,
		}
		if strings.TrimSpace(workspacePath) != "" {
			out["writableRoots"] = []string{workspacePath}
		}
		return out
	default:
		return map[string]any{"type": "readOnly"}
	}
}

// buildTurnInput builds the v2 turn input array. Text becomes {type:"text",text};
// local image attachments become {type:"localImage",path}.
func buildTurnInput(prompt string, images []string) []map[string]any {
	input := make([]map[string]any, 0, 1+len(images))
	if strings.TrimSpace(prompt) != "" {
		input = append(input, map[string]any{"type": "text", "text": prompt})
	}
	for _, image := range images {
		if strings.TrimSpace(image) == "" {
			continue
		}
		input = append(input, map[string]any{"type": "localImage", "path": image})
	}
	return input
}

// extractThreadID pulls a codex thread id out of a thread/start or thread/resume
// response: {result:{thread:{id}}}.
func extractThreadID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	payload := map[string]any{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if thread, ok := payload["thread"].(map[string]any); ok {
		for _, key := range []string{"id", "threadId", "thread_id"} {
			if value, ok := thread[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	for _, key := range []string{"threadId", "thread_id", "id"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// extractTurnID pulls a turn id out of a turn/start response: {result:{turn:{id}}}.
func extractTurnID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	payload := map[string]any{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if turn, ok := payload["turn"].(map[string]any); ok {
		for _, key := range []string{"id", "turnId", "turn_id"} {
			if value, ok := turn[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	for _, key := range []string{"turnId", "turn_id"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// CodexModel is a redacted model entry surfaced by model/list.
type CodexModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	IsDefault   bool   `json:"isDefault,omitempty"`
}

// parseModelList parses a model/list response: {result:{data:[{id,displayName,...}]}}.
func parseModelList(raw json.RawMessage) []CodexModel {
	payload := map[string]any{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	data, ok := payload["data"].([]any)
	if !ok {
		return nil
	}
	out := make([]CodexModel, 0, len(data))
	for _, entry := range data {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		id := firstString(item, "id", "model")
		if id == "" {
			continue
		}
		model := CodexModel{ID: id, DisplayName: firstString(item, "displayName")}
		if def, ok := item["isDefault"].(bool); ok {
			model.IsDefault = def
		}
		out = append(out, model)
	}
	return out
}
