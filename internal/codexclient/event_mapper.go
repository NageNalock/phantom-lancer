package codexclient

import (
	"encoding/json"
	"strings"
)

// Stable Phantom Lancer event types. The frontend renders only these; it never
// depends on raw Codex JSON-RPC method names or JSONL event shapes.
const (
	EventThreadStarted   = "thread.started"
	EventThreadResumed   = "thread.resumed"
	EventThreadArchived  = "thread.archived"
	EventTurnStarted     = "turn.started"
	EventTurnQueued      = "turn.queued"
	EventTurnCompleted   = "turn.completed"
	EventTurnFailed      = "turn.failed"
	EventTurnCancelled   = "turn.cancelled"
	EventMessageUser     = "message.user"
	EventMessageAgent    = "message.agent"
	EventMessageReason   = "message.reasoning"
	EventCommandStarted  = "command.started"
	EventCommandDone     = "command.completed"
	EventFileChangeStart = "file_change.started"
	EventFileChangeDone  = "file_change.completed"
	EventApprovalReq     = "approval.requested"
	EventApprovalResolve = "approval.resolved"
	EventToolStarted     = "tool.started"
	EventToolCompleted   = "tool.completed"
	EventPlanUpdated     = "plan.updated"
	EventDiffUpdated     = "diff.updated"
	EventStatusChanged   = "thread.status.changed"
	EventUsageUpdated    = "usage.updated"
	EventDiagWarning     = "diagnostic.warning"
	EventDiagError       = "diagnostic.error"
)

// Approval request methods are server-initiated JSON-RPC requests in the v2
// app-server protocol (openai/codex). They must be answered with a decision.
const (
	MethodCommandApproval = "item/commandExecution/requestApproval"
	MethodFileApproval    = "item/fileChange/requestApproval"
)

// MappedEvent is the normalized representation produced from a raw Codex
// notification (v2 app-server) or JSONL line (v1 exec).
type MappedEvent struct {
	EventType   string
	CodexMethod string
	ItemType    string
	TextPreview string
	Payload     map[string]any
	// TurnStatus, when non-empty, signals a terminal turn transition
	// (completed/failed/cancelled).
	TurnStatus string
}

// ApprovalRequest captures the redacted approval payload surfaced to the owner.
type ApprovalRequest struct {
	CodexRequestID string
	ActionKind     string
	CommandPreview string
	CwdSummary     string
	RiskLevel      string
}

// EventMapper converts Codex raw events into stable Phantom Lancer events.
type EventMapper struct {
	maxPreview int
}

func NewEventMapper(maxPreviewRunes int) *EventMapper {
	if maxPreviewRunes <= 0 {
		maxPreviewRunes = 2000
	}
	return &EventMapper{maxPreview: maxPreviewRunes}
}

// MapAppServerNotification maps a v2 app-server JSON-RPC notification. The v2
// protocol uses camelCase methods (turn/started, item/completed, …) and
// camelCase item types (commandExecution, agentMessage, …).
func (m *EventMapper) MapAppServerNotification(method string, params json.RawMessage) (MappedEvent, bool) {
	payload := map[string]any{}
	_ = json.Unmarshal(params, &payload)
	event := MappedEvent{CodexMethod: method, Payload: redactPayload(payload)}

	switch method {
	case "thread/started":
		event.EventType = EventThreadStarted
	case "thread/archived":
		event.EventType = EventThreadArchived
	case "thread/unarchived":
		event.EventType = EventThreadResumed
	case "thread/status/changed":
		event.EventType = EventStatusChanged
		event.TextPreview = Preview(extractStatusText(payload), m.maxPreview)
	case "turn/started":
		event.EventType = EventTurnStarted
	case "turn/completed":
		status := turnStatusFromV2(payload)
		switch status {
		case "interrupted":
			event.EventType = EventTurnCancelled
			event.TurnStatus = "cancelled"
		case "failed":
			event.EventType = EventTurnFailed
			event.TurnStatus = "failed"
			event.TextPreview = Preview(turnErrorFromV2(payload), m.maxPreview)
		default:
			event.EventType = EventTurnCompleted
			event.TurnStatus = "completed"
		}
	case "turn/plan/updated":
		event.EventType = EventPlanUpdated
		event.TextPreview = Preview(extractPlanText(payload), m.maxPreview)
	case "turn/diff/updated":
		event.EventType = EventDiffUpdated
		event.TextPreview = Preview(firstString(payload, "diff"), m.maxPreview)
	case "thread/tokenUsage/updated":
		event.EventType = EventUsageUpdated
		event.TextPreview = Preview(extractUsageText(payload), m.maxPreview)
	case "serverRequest/resolved":
		event.EventType = EventApprovalResolve
		event.TextPreview = "server request resolved"
	case "item/agentMessage/delta":
		event.EventType = EventMessageAgent
		event.ItemType = "agentMessage"
		event.TextPreview = Preview(extractText(payload), m.maxPreview)
	case "item/plan/delta":
		event.EventType = EventPlanUpdated
		event.ItemType = "plan"
		event.TextPreview = Preview(extractText(payload), m.maxPreview)
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		event.EventType = EventMessageReason
		event.ItemType = "reasoning"
		event.TextPreview = Preview(extractText(payload), m.maxPreview)
	case "item/commandExecution/outputDelta":
		event.EventType = EventCommandDone
		event.ItemType = "commandExecution"
		event.TextPreview = Preview(extractText(payload), m.maxPreview)
	case "item/started":
		return m.mapV2Item(method, payload, false)
	case "item/completed":
		return m.mapV2Item(method, payload, true)
	default:
		return MappedEvent{}, false
	}
	return event, true
}

func (m *EventMapper) mapV2Item(method string, payload map[string]any, completed bool) (MappedEvent, bool) {
	item, _ := payload["item"].(map[string]any)
	if item == nil {
		return MappedEvent{}, false
	}
	itemType := firstString(item, "type")
	event := MappedEvent{CodexMethod: method, ItemType: itemType, Payload: redactPayload(payload)}
	switch itemType {
	case "userMessage":
		if completed {
			return MappedEvent{}, false // user message already recorded locally on turn start
		}
		event.EventType = EventMessageUser
		event.TextPreview = Preview(extractContentText(item), m.maxPreview)
	case "agentMessage":
		event.EventType = EventMessageAgent
		event.TextPreview = Preview(firstString(item, "text"), m.maxPreview)
		if !completed && event.TextPreview == "" {
			return MappedEvent{}, false
		}
	case "reasoning":
		if !completed {
			return MappedEvent{}, false
		}
		event.EventType = EventMessageReason
		event.TextPreview = Preview(extractReasoningText(item), m.maxPreview)
	case "plan":
		event.EventType = EventPlanUpdated
		event.TextPreview = Preview(extractPlanItemText(item), m.maxPreview)
		if !completed && event.TextPreview == "" {
			return MappedEvent{}, false
		}
	case "commandExecution":
		if completed {
			event.EventType = EventCommandDone
			event.TextPreview = Preview(firstString(item, "aggregatedOutput", "output", "command"), m.maxPreview)
		} else {
			event.EventType = EventCommandStarted
			event.TextPreview = Preview(firstString(item, "command"), m.maxPreview)
		}
	case "fileChange":
		if completed {
			event.EventType = EventFileChangeDone
			event.TextPreview = Preview(extractFileChangeText(item), m.maxPreview)
		} else {
			event.EventType = EventFileChangeStart
			event.TextPreview = Preview(extractFileChangeText(item), m.maxPreview)
		}
	case "mcpToolCall", "dynamicToolCall", "collabToolCall", "webSearch", "imageView", "enteredReviewMode", "exitedReviewMode", "contextCompaction":
		if completed {
			event.EventType = EventToolCompleted
		} else {
			event.EventType = EventToolStarted
		}
	default:
		return MappedEvent{}, false
	}
	return event, true
}

// MapExecLine maps one JSONL line emitted by `codex exec --json`. The v1 exec
// schema uses snake_case event types (thread.started, item.completed, …) and
// snake_case item types (command_execution, agent_message, …).
func (m *EventMapper) MapExecLine(line []byte) (MappedEvent, bool) {
	payload := map[string]any{}
	if err := json.Unmarshal(line, &payload); err != nil {
		return MappedEvent{}, false
	}
	eventType := firstString(payload, "type")
	event := MappedEvent{CodexMethod: eventType, Payload: redactPayload(payload)}
	switch eventType {
	case "thread.started":
		event.EventType = EventThreadStarted
	case "turn.started":
		event.EventType = EventTurnStarted
	case "turn.completed":
		event.EventType = EventTurnCompleted
		event.TurnStatus = "completed"
	case "turn.failed":
		event.EventType = EventTurnFailed
		event.TurnStatus = "failed"
		event.TextPreview = Preview(turnErrorFromV2(payload), m.maxPreview)
	case "error":
		event.EventType = EventDiagError
		event.TextPreview = Preview(extractText(payload), m.maxPreview)
	case "item.started", "item.completed":
		return m.mapExecItem(eventType, payload)
	default:
		return MappedEvent{}, false
	}
	return event, true
}

func (m *EventMapper) mapExecItem(eventType string, payload map[string]any) (MappedEvent, bool) {
	item, _ := payload["item"].(map[string]any)
	if item == nil {
		return MappedEvent{}, false
	}
	completed := eventType == "item.completed"
	itemType := firstString(item, "type")
	event := MappedEvent{CodexMethod: eventType, ItemType: itemType, Payload: redactPayload(payload)}
	switch itemType {
	case "agent_message":
		event.EventType = EventMessageAgent
		event.TextPreview = Preview(firstString(item, "text"), m.maxPreview)
		if !completed {
			return MappedEvent{}, false
		}
	case "reasoning":
		if !completed {
			return MappedEvent{}, false
		}
		event.EventType = EventMessageReason
		event.TextPreview = Preview(firstString(item, "text"), m.maxPreview)
	case "command_execution":
		if completed {
			event.EventType = EventCommandDone
		} else {
			event.EventType = EventCommandStarted
			event.TextPreview = Preview(firstString(item, "command"), m.maxPreview)
		}
	case "file_change":
		if completed {
			event.EventType = EventFileChangeDone
			event.TextPreview = Preview(extractFileChangeText(item), m.maxPreview)
		} else {
			event.EventType = EventFileChangeStart
			event.TextPreview = Preview(extractFileChangeText(item), m.maxPreview)
		}
	case "mcp_tool_call", "web_search":
		if completed {
			event.EventType = EventToolCompleted
		} else {
			event.EventType = EventToolStarted
		}
	default:
		return MappedEvent{}, false
	}
	return event, true
}

// ParseApprovalRequest extracts a redacted ApprovalRequest from a server-initiated
// approval request. Returns false if the method is not an approval request.
func (m *EventMapper) ParseApprovalRequest(req ServerRequest) (ApprovalRequest, bool) {
	if req.Method != MethodCommandApproval && req.Method != MethodFileApproval {
		return ApprovalRequest{}, false
	}
	payload := map[string]any{}
	_ = json.Unmarshal(req.Params, &payload)
	approval := ApprovalRequest{
		CodexRequestID: firstString(payload, "itemId", "callId", "id"),
		RiskLevel:      "medium",
	}
	if req.Method == MethodFileApproval {
		approval.ActionKind = "file_change"
		approval.CommandPreview = Preview(firstString(payload, "reason"), 400)
		approval.CwdSummary = Preview(firstString(payload, "grantRoot", "cwd"), 200)
	} else {
		approval.ActionKind = "command"
		approval.CommandPreview = Preview(firstString(payload, "command", "reason"), 400)
		approval.CwdSummary = Preview(firstString(payload, "cwd"), 200)
		if _, ok := payload["networkApprovalContext"]; ok {
			approval.ActionKind = "network"
			approval.RiskLevel = "high"
		}
	}
	lower := strings.ToLower(approval.CommandPreview)
	if strings.Contains(lower, "rm ") || strings.Contains(lower, "delete") || strings.Contains(lower, "curl") || strings.Contains(lower, "wget") || strings.Contains(lower, "sudo") {
		approval.RiskLevel = "high"
	}
	return approval, true
}

func turnStatusFromV2(payload map[string]any) string {
	if turn, ok := payload["turn"].(map[string]any); ok {
		return firstString(turn, "status")
	}
	return firstString(payload, "status")
}

func turnErrorFromV2(payload map[string]any) string {
	if errObj, ok := payload["error"].(map[string]any); ok {
		if msg := firstString(errObj, "message"); msg != "" {
			return msg
		}
	}
	if turn, ok := payload["turn"].(map[string]any); ok {
		if errObj, ok := turn["error"].(map[string]any); ok {
			return firstString(errObj, "message")
		}
	}
	return ""
}

func extractStatusText(payload map[string]any) string {
	if status, ok := payload["status"].(map[string]any); ok {
		if typ := firstString(status, "type"); typ != "" {
			return typ
		}
	}
	return firstString(payload, "status", "type")
}

func extractPlanText(payload map[string]any) string {
	if plan, ok := payload["plan"].([]any); ok {
		parts := make([]string, 0, len(plan))
		for _, raw := range plan {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			step := firstString(item, "step", "text")
			status := firstString(item, "status")
			if step == "" {
				continue
			}
			if status != "" {
				parts = append(parts, status+": "+step)
			} else {
				parts = append(parts, step)
			}
		}
		return strings.Join(parts, "\n")
	}
	return firstString(payload, "plan", "text", "delta")
}

func extractUsageText(payload map[string]any) string {
	data, err := json.Marshal(redactPayload(payload))
	if err != nil {
		return ""
	}
	return string(data)
}

func extractPlanItemText(item map[string]any) string {
	return firstString(item, "text", "summary", "content")
}

func extractFileChangeText(item map[string]any) string {
	if text := firstString(item, "diff", "text", "summary"); text != "" {
		return text
	}
	changes, ok := item["changes"].([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(changes))
	for _, raw := range changes {
		change, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		path := firstString(change, "path")
		kind := firstString(change, "kind")
		diff := firstString(change, "diff")
		header := strings.TrimSpace(strings.Join([]string{kind, path}, " "))
		if diff != "" {
			if header != "" {
				parts = append(parts, header+"\n"+diff)
			} else {
				parts = append(parts, diff)
			}
		} else if header != "" {
			parts = append(parts, header)
		}
	}
	return strings.Join(parts, "\n\n")
}

func redactPayload(payload map[string]any) map[string]any {
	data, err := json.Marshal(payload)
	if err != nil {
		return map[string]any{}
	}
	redacted := Redact(string(data), 0)
	out := map[string]any{}
	if err := json.Unmarshal([]byte(redacted), &out); err != nil {
		// Redaction may break JSON if it touched structural characters; fall back
		// to a minimal safe payload.
		return map[string]any{"_redacted": true}
	}
	return out
}

func extractText(payload map[string]any) string {
	for _, key := range []string{"text", "message", "content", "delta", "summary"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	if nested, ok := payload["message"].(map[string]any); ok {
		if value, ok := nested["content"].(string); ok {
			return value
		}
	}
	if errObj, ok := payload["error"].(map[string]any); ok {
		return firstString(errObj, "message")
	}
	return ""
}

// extractContentText reads the text of a v2 userMessage item whose content is a
// list of typed input parts ({type:"text",text:...}).
func extractContentText(item map[string]any) string {
	if text := firstString(item, "text"); text != "" {
		return text
	}
	content, ok := item["content"].([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(content))
	for _, raw := range content {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if text := firstString(part, "text"); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// extractReasoningText reads streamed reasoning summary/content from a v2
// reasoning item.
func extractReasoningText(item map[string]any) string {
	for _, key := range []string{"summary", "content", "text"} {
		if text := firstString(item, key); text != "" {
			return text
		}
		if list, ok := item[key].([]any); ok {
			parts := make([]string, 0, len(list))
			for _, raw := range list {
				if s, ok := raw.(string); ok {
					parts = append(parts, s)
				} else if part, ok := raw.(map[string]any); ok {
					if t := firstString(part, "text"); t != "" {
						parts = append(parts, t)
					}
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
		}
	}
	return ""
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
