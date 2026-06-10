package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"phantom-lancer/internal/storage"
)

type codexGatewayAccountExportPayload struct {
	Version    int                              `json:"version"`
	ExportedAt string                           `json:"exportedAt"`
	Accounts   []codexGatewayAccountExportEntry `json:"accounts"`
}

type codexGatewayAccountExportEntry struct {
	ID           string `json:"id,omitempty"`
	Label        string `json:"label,omitempty"`
	Status       string `json:"status,omitempty"`
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	Plan         string `json:"plan,omitempty"`
	CreatedAt    string `json:"createdAt,omitempty"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
}

type codexGatewayAccountImportEntry struct {
	ID           string
	Label        string
	Status       string
	AccessToken  string
	RefreshToken string
	ExpiresAt    string
	Plan         string
}

type codexGatewayAccountImportResult struct {
	Success bool     `json:"success"`
	Added   int      `json:"added"`
	Updated int      `json:"updated"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors"`
}

func (s *Server) handleExportCodexGatewayAccounts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	idFilter := map[string]bool{}
	for _, id := range splitCSV(r.URL.Query().Get("ids")) {
		idFilter[id] = true
	}
	accounts, err := s.store.ListCodexGatewayAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	entries := make([]codexGatewayAccountExportEntry, 0, len(accounts))
	for _, account := range accounts {
		if len(idFilter) > 0 && !idFilter[account.ID] {
			continue
		}
		secret, err := s.store.GetCodexGatewayAccountSecret(r.Context(), account.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		entries = append(entries, codexGatewayAccountExportEntry{
			ID:           secret.ID,
			Label:        secret.Label,
			Status:       secret.Status,
			AccessToken:  secret.AccessToken,
			RefreshToken: secret.RefreshToken,
			ExpiresAt:    secret.ExpiresAt,
			Plan:         secret.Plan,
			CreatedAt:    secret.CreatedAt,
			UpdatedAt:    secret.UpdatedAt,
		})
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "codex_gateway.account.exported",
		RiskLevel: "high",
		Summary:   "已导出 Codex Gateway 账号配置",
		Payload:   map[string]any{"count": len(entries)},
	})
	filename := "codex-gateway-accounts-" + time.Now().UTC().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	writeJSON(w, http.StatusOK, codexGatewayAccountExportPayload{
		Version:    1,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Accounts:   entries,
	})
}

func (s *Server) handleImportCodexGatewayAccounts(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	entries, err := parseCodexGatewayImportRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_import", err.Error())
		return
	}
	if len(entries) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_import", "没有可导入的账号")
		return
	}
	result := s.importCodexGatewayAccounts(r.Context(), entries)
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "codex_gateway.account.imported",
		RiskLevel: "high",
		Summary:   "已导入 Codex Gateway 账号配置",
		Payload:   map[string]any{"added": result.Added, "updated": result.Updated, "failed": result.Failed},
	})
	status := http.StatusOK
	if result.Added+result.Updated == 0 {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (s *Server) importCodexGatewayAccounts(ctx context.Context, entries []codexGatewayAccountImportEntry) codexGatewayAccountImportResult {
	result := codexGatewayAccountImportResult{Success: true, Errors: []string{}}
	for index, entry := range entries {
		entry.Label = strings.TrimSpace(entry.Label)
		entry.Status = strings.TrimSpace(entry.Status)
		entry.AccessToken = normalizeBearer(entry.AccessToken)
		entry.RefreshToken = strings.TrimSpace(entry.RefreshToken)
		if entry.AccessToken == "" && entry.RefreshToken == "" {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("第 %d 个账号缺少 token", index+1))
			continue
		}
		if entry.Status == "" {
			entry.Status = "active"
		}
		if !validGatewayAccountStatus(entry.Status) {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("第 %d 个账号状态不合法", index+1))
			continue
		}
		updated, err := s.upsertImportedCodexGatewayAccount(ctx, entry)
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("第 %d 个账号保存失败", index+1))
			continue
		}
		if updated {
			result.Updated++
		} else {
			result.Added++
		}
	}
	if result.Failed > 0 {
		result.Success = false
	}
	return result
}

func (s *Server) upsertImportedCodexGatewayAccount(ctx context.Context, entry codexGatewayAccountImportEntry) (bool, error) {
	if entry.ID != "" {
		if _, err := s.store.GetCodexGatewayAccount(ctx, entry.ID); err == nil {
			patch := storage.CodexGatewayAccountPatch{
				Label:     &entry.Label,
				Status:    &entry.Status,
				ExpiresAt: &entry.ExpiresAt,
				Plan:      &entry.Plan,
			}
			if entry.AccessToken != "" {
				patch.AccessToken = &entry.AccessToken
			}
			if entry.RefreshToken != "" {
				patch.RefreshToken = &entry.RefreshToken
			}
			_, err := s.store.UpdateCodexGatewayAccount(ctx, entry.ID, patch)
			return true, err
		}
	}
	_, err := s.store.CreateCodexGatewayAccount(ctx, storage.CodexGatewayAccountInput{
		Label:        entry.Label,
		Status:       entry.Status,
		AccessToken:  entry.AccessToken,
		RefreshToken: entry.RefreshToken,
		ExpiresAt:    entry.ExpiresAt,
		Plan:         entry.Plan,
	})
	return false, err
}

func parseCodexGatewayImportRequest(r *http.Request) ([]codexGatewayAccountImportEntry, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("读取导入内容失败")
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, fmt.Errorf("导入内容为空")
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return parseCodexGatewayImportText(text), nil
	}
	return parseCodexGatewayImportValue(payload), nil
}

func parseCodexGatewayImportText(text string) []codexGatewayAccountImportEntry {
	var entries []codexGatewayAccountImportEntry
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var payload any
		if json.Unmarshal([]byte(line), &payload) == nil {
			entries = append(entries, parseCodexGatewayImportValue(payload)...)
			continue
		}
		if entry, ok := importEntryFromToken(line); ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

func parseCodexGatewayImportValue(value any) []codexGatewayAccountImportEntry {
	switch typed := value.(type) {
	case []any:
		var entries []codexGatewayAccountImportEntry
		for _, item := range typed {
			entries = append(entries, parseCodexGatewayImportValue(item)...)
		}
		return entries
	case string:
		if entry, ok := importEntryFromToken(typed); ok {
			return []codexGatewayAccountImportEntry{entry}
		}
		return nil
	case map[string]any:
		if accounts, ok := typed["accounts"].([]any); ok {
			var entries []codexGatewayAccountImportEntry
			for _, item := range accounts {
				entries = append(entries, parseCodexGatewayImportValue(item)...)
			}
			return entries
		}
		entry := importEntryFromRecord(typed)
		if entry.AccessToken == "" && entry.RefreshToken == "" {
			return nil
		}
		return []codexGatewayAccountImportEntry{entry}
	default:
		return nil
	}
}

func importEntryFromRecord(record map[string]any) codexGatewayAccountImportEntry {
	entry := codexGatewayAccountImportEntry{
		ID:           firstImportString(record, "id", "account_id", "accountId"),
		Label:        firstImportString(record, "label", "name", "account_name", "accountName", "note"),
		Status:       firstImportString(record, "status"),
		AccessToken:  normalizeBearer(firstImportString(record, "access_token", "accessToken", "token")),
		RefreshToken: firstImportString(record, "refresh_token", "refreshToken"),
		ExpiresAt:    firstImportString(record, "expires_at", "expiresAt", "expired"),
		Plan:         firstImportString(record, "plan", "plan_type", "planType", "tier"),
	}
	if tokens, ok := record["tokens"].(map[string]any); ok {
		if entry.AccessToken == "" {
			entry.AccessToken = normalizeBearer(firstImportString(tokens, "access_token", "accessToken", "token"))
		}
		if entry.RefreshToken == "" {
			entry.RefreshToken = firstImportString(tokens, "refresh_token", "refreshToken")
		}
	}
	return entry
}

func importEntryFromToken(value string) (codexGatewayAccountImportEntry, bool) {
	token := normalizeBearer(value)
	if token == "" {
		return codexGatewayAccountImportEntry{}, false
	}
	if strings.HasPrefix(token, "oaistb_rt_") || strings.HasPrefix(token, "rt_") {
		return codexGatewayAccountImportEntry{RefreshToken: token}, true
	}
	return codexGatewayAccountImportEntry{AccessToken: token}, true
}

func firstImportString(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := record[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func splitCSV(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}
