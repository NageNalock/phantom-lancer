package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/mail/configapply"
	"phantom-lancer/internal/storage"
)

const emergencyInboundRejectKey = "emergency_inbound_reject"

type EmergencyInboundRejectState struct {
	Enabled                  bool   `json:"enabled"`
	Reason                   string `json:"reason,omitempty"`
	Mode                     string `json:"mode"`
	AppliedBy                string `json:"applied_by,omitempty"`
	AppliedAt                string `json:"applied_at,omitempty"`
	AutoRestoreAt            string `json:"auto_restore_at,omitempty"`
	LastAutoRestoreAttemptAt string `json:"last_auto_restore_attempt_at,omitempty"`
	AutoRestoreBlockedAt     string `json:"auto_restore_blocked_at,omitempty"`
	LastNormalConfigHash     string `json:"last_normal_config_hash,omitempty"`
	LastConfigHash           string `json:"last_config_hash,omitempty"`
	LastApplySummary         string `json:"last_apply_summary,omitempty"`
	LastFailure              string `json:"last_failure,omitempty"`
	LastFailureStep          int    `json:"last_failure_step,omitempty"`
	LastRollbackResult       string `json:"last_rollback_result,omitempty"`
	LastReloadResult         string `json:"last_reload_result,omitempty"`
	LastProbeResult          string `json:"last_probe_result,omitempty"`
	RestoreConflict          string `json:"restore_conflict,omitempty"`
	RestoreExpectedHash      string `json:"restore_expected_hash,omitempty"`
	RestoreDiskHash          string `json:"restore_disk_hash,omitempty"`
	ApplyUnknown             bool   `json:"apply_unknown,omitempty"`
	AffectedDomains          int    `json:"affected_domains"`
	AffectedAccounts         int    `json:"affected_accounts"`
	ActualMoxStrategy        string `json:"actual_mox_strategy"`
	DegradedImplementation   bool   `json:"degraded_implementation"`
	DegradedReason           string `json:"degraded_reason,omitempty"`
}

type EmergencyInboundRejectRequest struct {
	Reason        string `json:"reason"`
	Confirmation  string `json:"confirmation"`
	AutoRestoreAt string `json:"auto_restore_at,omitempty"`
}

func (s *Service) EmergencyInboundRejectGet(ctx context.Context) (*EmergencyInboundRejectState, error) {
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil {
		return nil, err
	}
	state := emergencyStateFromSettings(settings)
	s.enrichEmergencyCounts(ctx, state)
	return state, nil
}

func (s *Service) EmergencyInboundRejectEnable(ctx context.Context, req EmergencyInboundRejectRequest) (*EmergencyInboundRejectState, *configapply.PipelineResult, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(req.Confirmation) != "REJECT-INBOUND" {
		return nil, nil, errors.New("confirmation must be REJECT-INBOUND")
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return nil, nil, errors.New("reason is required")
	}
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil {
		return nil, nil, err
	}
	state := emergencyStateFromSettings(settings)
	if state.Enabled {
		s.enrichEmergencyCounts(ctx, state)
		return state, nil, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	state.Enabled = true
	state.Reason = reason
	state.Mode = "domain_disabled_fallback"
	state.AppliedBy = "owner"
	state.AppliedAt = now
	state.AutoRestoreAt = strings.TrimSpace(req.AutoRestoreAt)
	state.LastAutoRestoreAttemptAt = ""
	state.AutoRestoreBlockedAt = ""
	state.LastNormalConfigHash = s.currentConfigHash()
	state.LastFailure = ""
	state.LastFailureStep = 0
	state.LastRollbackResult = ""
	state.LastReloadResult = ""
	state.LastProbeResult = ""
	state.RestoreConflict = ""
	state.RestoreExpectedHash = ""
	state.RestoreDiskHash = ""
	state.ApplyUnknown = false
	state.ActualMoxStrategy = "mox_domain_disabled"
	state.DegradedImplementation = true
	state.DegradedReason = emergencyDegradedReason
	if err := s.persistEmergencyState(ctx, settings, state); err != nil {
		return nil, nil, err
	}
	pr := s.applyFromDomains(ctx)
	state.capturePipelineResult(pr)
	if pr == nil || !pr.Success {
		if emergencyApplyUnknown(pr) {
			state.Enabled = true
			state.ApplyUnknown = true
			state.RestoreConflict = "apply_failed_rollback_unknown"
			state.LastFailure = pipelineSummary(pr)
			if s.drift != nil {
				state.RestoreExpectedHash = s.drift.SQLiteHash()
				state.RestoreDiskHash = s.drift.DiskHash()
			}
		} else {
			state.Enabled = false
			state.ApplyUnknown = false
			state.LastFailure = pipelineSummary(pr)
		}
		_ = s.persistEmergencyState(ctx, settings, state)
		s.recordEmergencyFailure(ctx, "enable", reason, state, pr)
		return state, pr, fmt.Errorf("emergency inbound reject apply failed: %s", state.LastFailure)
	}
	state.LastConfigHash = pr.ConfigHash
	state.LastApplySummary = pr.Summary
	state.LastFailure = ""
	state.LastFailureStep = 0
	state.ApplyUnknown = false
	if err := s.persistEmergencyState(ctx, settings, state); err != nil {
		return nil, pr, err
	}
	s.enrichEmergencyCounts(ctx, state)
	s.addAudit(ctx, EventTypeEmergencyUpdated, "enabled domain-disabled fallback protection",
		state.auditPayload("enable", reason, pr),
		"high")
	s.publish(ctx, EventTypeEmergencyUpdated, state.eventPayload("enable", pr))
	s.touchLastChange()
	return state, pr, nil
}

func (s *Service) EmergencyInboundRejectDisable(ctx context.Context, req EmergencyInboundRejectRequest) (*EmergencyInboundRejectState, *configapply.PipelineResult, error) {
	if strings.TrimSpace(req.Confirmation) != "RESTORE-INBOUND" {
		return nil, nil, errors.New("confirmation must be RESTORE-INBOUND")
	}
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil {
		return nil, nil, err
	}
	if settings != nil && settings.ImportMode {
		return nil, nil, &errCoded{code: "import_read_only", msg: "import mode: writes are disabled"}
	}
	state := emergencyStateFromSettings(settings)
	if !state.Enabled {
		s.enrichEmergencyCounts(ctx, state)
		return state, nil, nil
	}
	if s.Drifted() {
		state.RestoreConflict = "config_drifted"
		if s.drift != nil {
			state.RestoreExpectedHash = s.drift.SQLiteHash()
			state.RestoreDiskHash = s.drift.DiskHash()
		}
		state.LastFailure = "config drifted during emergency; resolve drift before restoring inbound"
		_ = s.persistEmergencyState(ctx, settings, state)
		s.recordEmergencyFailure(ctx, "disable", strings.TrimSpace(req.Reason), state, nil)
		return state, nil, &errCoded{code: "config_drifted", msg: state.LastFailure}
	}
	if err := s.checkWriteGuard(ctx); err != nil {
		return state, nil, err
	}
	state.Enabled = false
	state.LastFailure = ""
	state.LastFailureStep = 0
	state.LastRollbackResult = ""
	state.LastReloadResult = ""
	state.LastProbeResult = ""
	state.RestoreConflict = ""
	state.RestoreExpectedHash = ""
	state.RestoreDiskHash = ""
	state.ApplyUnknown = false
	if strings.TrimSpace(req.Reason) != "" {
		state.Reason = strings.TrimSpace(req.Reason)
	}
	if err := s.persistEmergencyState(ctx, settings, state); err != nil {
		return nil, nil, err
	}
	pr := s.applyFromDomains(ctx)
	state.capturePipelineResult(pr)
	if pr == nil || !pr.Success {
		state.Enabled = true
		state.LastFailure = pipelineSummary(pr)
		_ = s.persistEmergencyState(ctx, settings, state)
		s.recordEmergencyFailure(ctx, "disable", strings.TrimSpace(req.Reason), state, pr)
		return state, pr, fmt.Errorf("emergency inbound restore apply failed: %s", state.LastFailure)
	}
	state.LastConfigHash = pr.ConfigHash
	state.LastApplySummary = pr.Summary
	state.LastFailure = ""
	state.LastFailureStep = 0
	state.ApplyUnknown = false
	state.LastNormalConfigHash = pr.ConfigHash
	state.AutoRestoreAt = ""
	state.LastAutoRestoreAttemptAt = ""
	state.AutoRestoreBlockedAt = ""
	if err := s.persistEmergencyState(ctx, settings, state); err != nil {
		return nil, pr, err
	}
	s.enrichEmergencyCounts(ctx, state)
	s.addAudit(ctx, EventTypeEmergencyUpdated, "disabled domain-disabled fallback protection",
		state.auditPayload("disable", strings.TrimSpace(req.Reason), pr),
		"high")
	s.publish(ctx, EventTypeEmergencyUpdated, state.eventPayload("disable", pr))
	s.touchLastChange()
	return state, pr, nil
}

func emergencyStateFromSettings(settings *storage.MailMoxSettings) *EmergencyInboundRejectState {
	state := &EmergencyInboundRejectState{
		Mode:                   "domain_disabled_fallback",
		ActualMoxStrategy:      "mox_domain_disabled",
		DegradedImplementation: true,
		DegradedReason:         emergencyDegradedReason,
	}
	if settings == nil || strings.TrimSpace(settings.ExtraCapabilitiesJSON) == "" {
		return state
	}
	var caps map[string]json.RawMessage
	if err := json.Unmarshal([]byte(settings.ExtraCapabilitiesJSON), &caps); err != nil {
		return state
	}
	if raw := caps[emergencyInboundRejectKey]; len(raw) > 0 {
		_ = json.Unmarshal(raw, state)
	}
	if state.Mode == "" {
		state.Mode = "domain_disabled_fallback"
	}
	if state.ActualMoxStrategy == "" {
		state.ActualMoxStrategy = "mox_domain_disabled"
	}
	state.DegradedImplementation = true
	if state.DegradedReason == "" {
		state.DegradedReason = emergencyDegradedReason
	}
	return state
}

func (s *Service) persistEmergencyState(ctx context.Context, settings *storage.MailMoxSettings, state *EmergencyInboundRejectState) error {
	caps := map[string]json.RawMessage{}
	if settings != nil && strings.TrimSpace(settings.ExtraCapabilitiesJSON) != "" {
		_ = json.Unmarshal([]byte(settings.ExtraCapabilitiesJSON), &caps)
	}
	buf, _ := json.Marshal(state)
	caps[emergencyInboundRejectKey] = buf
	next, _ := json.Marshal(caps)
	_, err := s.store.MailUpdateExtraCapabilities(ctx, string(next))
	return err
}

func (s *Service) enrichEmergencyCounts(ctx context.Context, state *EmergencyInboundRejectState) {
	if state == nil || s.store == nil {
		return
	}
	state.AffectedDomains = 0
	state.AffectedAccounts = 0
	if domains, err := s.store.MailListDomains(ctx); err == nil {
		for _, d := range domains {
			if d != nil && d.Enabled {
				state.AffectedDomains++
			}
		}
	}
	if accounts, err := s.store.MailListAccounts(ctx, "", ""); err == nil {
		for _, a := range accounts {
			if a.Status == "" || a.Status == "active" {
				state.AffectedAccounts++
			}
		}
	}
}

const emergencyDegradedReason = "Current adapter uses Mox Domain.Disabled as a controlled fallback; this may also affect submission and ACME for affected domains until a true early SMTP reject hook is wired."

func (s *Service) currentConfigHash() string {
	if s != nil && s.drift != nil {
		return s.drift.SQLiteHash()
	}
	return ""
}

func pipelineSummary(pr *configapply.PipelineResult) string {
	if pr == nil {
		return "configapply returned nil"
	}
	if strings.TrimSpace(pr.Summary) != "" {
		return pr.Summary
	}
	return "configapply failed"
}

func (state *EmergencyInboundRejectState) capturePipelineResult(pr *configapply.PipelineResult) {
	if state == nil || pr == nil {
		return
	}
	state.LastApplySummary = pr.Summary
	state.LastFailureStep = pr.FailureStep
	state.LastRollbackResult = "not_required"
	if pr.FailureStep >= 7 {
		if pr.RolledBack {
			state.LastRollbackResult = "rolled_back"
		} else if pr.RollbackErr != "" {
			state.LastRollbackResult = "rollback_failed: " + truncateEmergencyText(pr.RollbackErr, 180)
		} else {
			state.LastRollbackResult = "rollback_not_confirmed"
		}
	}
	for _, step := range pr.Steps {
		switch step.Name {
		case "ReloadOrRestart":
			state.LastReloadResult = step.State
			if step.Message != "" {
				state.LastReloadResult += ": " + truncateEmergencyText(step.Message, 160)
			}
		case "ProbeLayersL1_L2_L3":
			state.LastProbeResult = step.State
			if step.Message != "" {
				state.LastProbeResult += ": " + truncateEmergencyText(step.Message, 160)
			}
		}
	}
}

func (state *EmergencyInboundRejectState) auditPayload(action, reason string, pr *configapply.PipelineResult) map[string]any {
	payload := state.eventPayload(action, pr)
	payload["operator"] = state.AppliedBy
	payload["reason_summary"] = truncateEmergencyText(reason, 180)
	payload["reason_len"] = len(reason)
	payload["expected_reject_mode"] = state.Mode
	payload["rollback_result"] = state.LastRollbackResult
	payload["reload_result"] = state.LastReloadResult
	payload["probe_result"] = state.LastProbeResult
	payload["last_normal_config_hash"] = state.LastNormalConfigHash
	payload["restore_conflict"] = state.RestoreConflict
	payload["restore_expected_hash"] = state.RestoreExpectedHash
	payload["restore_disk_hash"] = state.RestoreDiskHash
	payload["apply_unknown"] = state.ApplyUnknown
	return payload
}

func (state *EmergencyInboundRejectState) eventPayload(action string, pr *configapply.PipelineResult) map[string]any {
	payload := map[string]any{
		"action":                  action,
		"enabled":                 state.Enabled,
		"mode":                    state.Mode,
		"actual_mox_strategy":     state.ActualMoxStrategy,
		"degraded_implementation": state.DegradedImplementation,
		"domains":                 state.AffectedDomains,
		"accounts":                state.AffectedAccounts,
		"last_failure":            state.LastFailure,
		"failure_step":            state.LastFailureStep,
		"auto_restore_at":         state.AutoRestoreAt,
		"auto_restore_blocked_at": state.AutoRestoreBlockedAt,
	}
	if pr != nil {
		payload["pipeline_success"] = pr.Success
		payload["pipeline_summary"] = pr.Summary
		payload["config_hash"] = pr.ConfigHash
		payload["rolled_back"] = pr.RolledBack
		payload["rollback_err"] = truncateEmergencyText(pr.RollbackErr, 180)
	}
	return payload
}

func (s *Service) recordEmergencyFailure(ctx context.Context, action, reason string, state *EmergencyInboundRejectState, pr *configapply.PipelineResult) {
	if state == nil {
		return
	}
	s.enrichEmergencyCounts(ctx, state)
	s.addAudit(ctx, EventTypeEmergencyUpdated, "domain-disabled fallback "+action+" failed", state.auditPayload(action+"_failed", reason, pr), "high")
	s.publish(ctx, EventTypeEmergencyUpdated, state.eventPayload(action+"_failed", pr))
}

func truncateEmergencyText(v string, max int) string {
	v = strings.TrimSpace(v)
	if max <= 0 || len(v) <= max {
		return v
	}
	return v[:max] + "..."
}

func emergencyApplyUnknown(pr *configapply.PipelineResult) bool {
	if pr == nil {
		return false
	}
	if pr.FailureStep < 7 {
		return false
	}
	return !pr.RolledBack || strings.TrimSpace(pr.RollbackErr) != ""
}

func (s *Service) runEmergencyAutoRestoreTick(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil || settings == nil {
		return
	}
	state := emergencyStateFromSettings(settings)
	if !state.Enabled || strings.TrimSpace(state.AutoRestoreAt) == "" {
		return
	}
	deadline, err := time.Parse(time.RFC3339, strings.TrimSpace(state.AutoRestoreAt))
	if err != nil || time.Now().UTC().Before(deadline) {
		return
	}
	if state.AutoRestoreBlockedAt == state.AutoRestoreAt {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	state.LastAutoRestoreAttemptAt = now
	_ = s.persistEmergencyState(ctx, settings, state)
	s.publish(ctx, EventTypeEmergencyUpdated, state.eventPayload("auto_restore_due", nil))
	_, pr, derr := s.EmergencyInboundRejectDisable(ctx, EmergencyInboundRejectRequest{
		Reason:       "auto restore deadline reached",
		Confirmation: "RESTORE-INBOUND",
	})
	if derr != nil {
		settingsAfter, gerr := s.store.MailGetSettings(ctx)
		if gerr == nil {
			state = emergencyStateFromSettings(settingsAfter)
		}
		state.Enabled = true
		state.LastAutoRestoreAttemptAt = now
		state.AutoRestoreBlockedAt = state.AutoRestoreAt
		state.LastFailure = truncateEmergencyText(derr.Error(), 240)
		state.capturePipelineResult(pr)
		_ = s.persistEmergencyState(ctx, settingsAfter, state)
		s.recordEmergencyFailure(ctx, "auto_restore", "auto restore deadline reached", state, pr)
		s.log.WarnContext(ctx, "mail: emergency auto restore failed", "error", derr)
	}
}
