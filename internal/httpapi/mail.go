package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/mail"
	"phantom-lancer/internal/mail/configapply"
	"phantom-lancer/internal/storage"
)

const maxMailAttachmentDownloadBytes int64 = 50 << 20

// errMailNotWired is returned by mail helpers when the Server was built
// without a mail.Service (unit test convenience).
var errMailNotWired = errors.New("mail service not wired into httpapi")

// registerMailRoutes wires every handler under /api/mail/.  The handlers are
// grouped logically (binary/setup/runtime/domains/accounts/...) to match the
// plan phase they ship in; Phase 1 only exposes a single status endpoint so
// the UI can confirm wiring.
func (s *Server) registerMailRoutes(mux *http.ServeMux) {
	// Phase 1 — minimal wiring smoke test.
	mux.HandleFunc("GET /api/mail/status", s.handleMailStatus)

	// --- Phase 2.4: Binary actions -----------------------------------------
	// All three are idempotent-ish reads or controlled writes (only Install
	// mutates FS state; Download writes tempfiles that are GC'd on boot).
	mux.HandleFunc("POST /api/mail/binary/detect", s.handleMailBinaryDetect)
	mux.HandleFunc("POST /api/mail/binary/download", s.handleMailBinaryDownload)
	mux.HandleFunc("POST /api/mail/binary/install", s.handleMailBinaryInstall)
	mux.HandleFunc("POST /api/mail/binary/uninstall", s.handleMailBinaryUninstall)

	// --- Phase 2.4: Setup actions ------------------------------------------
	mux.HandleFunc("POST /api/mail/setup/initialize", s.handleMailSetupInitialize)
	mux.HandleFunc("POST /api/mail/setup/import", s.handleMailSetupImport)
	mux.HandleFunc("POST /api/mail/setup/preflight-ports", s.handleMailSetupPreflightPorts)

	// --- Phase 2.4: Runtime lifecycle --------------------------------------
	// GET status is read-only; POST start/stop/restart are destructive writes.
	mux.HandleFunc("GET /api/mail/runtime/status", s.handleMailRuntimeStatus)
	mux.HandleFunc("POST /api/mail/runtime/start", s.handleMailRuntimeStart)
	mux.HandleFunc("POST /api/mail/runtime/stop", s.handleMailRuntimeStop)
	mux.HandleFunc("POST /api/mail/runtime/restart", s.handleMailRuntimeRestart)
	mux.HandleFunc("POST /api/mail/runtime/probe", s.handleMailRuntimeProbe)

	// --- Phase 3: Config application + drift -------------------------------
	mux.HandleFunc("POST /api/mail/config/validate", s.handleMailConfigValidate)
	mux.HandleFunc("POST /api/mail/config/apply", s.handleMailConfigApply)
	mux.HandleFunc("POST /api/mail/config/rollback", s.handleMailConfigRollback)
	mux.HandleFunc("GET /api/mail/config/summary", s.handleMailConfigSummary)
	mux.HandleFunc("POST /api/mail/runtime/resolve-drift", s.handleMailResolveDrift)
	mux.HandleFunc("GET /api/mail/emergency/inbound-reject", s.handleMailEmergencyInboundRejectGet)
	mux.HandleFunc("POST /api/mail/emergency/inbound-reject/enable", s.handleMailEmergencyInboundRejectEnable)
	mux.HandleFunc("POST /api/mail/emergency/inbound-reject/disable", s.handleMailEmergencyInboundRejectDisable)
	// --- Phase 3: Domains --------------------------------------------------
	mux.HandleFunc("GET /api/mail/domains", s.handleMailDomainList)
	mux.HandleFunc("POST /api/mail/domains", s.handleMailDomainCreate)
	mux.HandleFunc("GET /api/mail/domains/{id}", s.handleMailDomainGet)
	mux.HandleFunc("PUT /api/mail/domains/{id}", s.handleMailDomainUpdate)
	mux.HandleFunc("DELETE /api/mail/domains/{id}", s.handleMailDomainDelete)
	mux.HandleFunc("POST /api/mail/domains/{id}/enable", s.handleMailDomainEnable)
	mux.HandleFunc("POST /api/mail/domains/{id}/disable", s.handleMailDomainDisable)
	mux.HandleFunc("POST /api/mail/domains/{id}/dns-check", s.handleMailDomainDNSCheck)
	mux.HandleFunc("GET /api/mail/domains/{id}/dns-records", s.handleMailDomainDNSRecords)

	// --- Phase 4: CertManager (DNS providers + Certificates + Manual challenges)
	// Certificates — 6 routes (1 list/read, 5 writes).
	mux.HandleFunc("GET /api/mail/certificates", s.handleMailCertificateList)
	mux.HandleFunc("POST /api/mail/certificates", s.handleMailCertificateIssue)
	mux.HandleFunc("GET /api/mail/certificates/{id}", s.handleMailCertificateGet)
	mux.HandleFunc("POST /api/mail/certificates/{id}/renew", s.handleMailCertificateRenew)
	mux.HandleFunc("POST /api/mail/certificates/{id}/rollback", s.handleMailCertificateRollback)
	mux.HandleFunc("DELETE /api/mail/certificates/{id}", s.handleMailCertificateDelete)

	// DNS Providers — 4 routes (1 read, 3 writes + test).
	mux.HandleFunc("GET /api/mail/dns-providers", s.handleMailDNSProviderList)
	mux.HandleFunc("POST /api/mail/dns-providers", s.handleMailDNSProviderUpsert)
	mux.HandleFunc("DELETE /api/mail/dns-providers/{id}", s.handleMailDNSProviderDelete)
	mux.HandleFunc("POST /api/mail/dns-providers/{id}/test", s.handleMailDNSProviderTest)

	// Manual DNS-01 challenges — 3 routes (1 read, 2 writes).
	mux.HandleFunc("GET /api/mail/manual-challenges", s.handleMailManualChallengeList)
	mux.HandleFunc("POST /api/mail/manual-challenges/{id}/confirm", s.handleMailManualChallengeConfirm)
	mux.HandleFunc("DELETE /api/mail/manual-challenges/{id}", s.handleMailManualChallengeCancel)

	// ---- Group 5: Accounts + Aliases + Import ----
	// Accounts — 8 routes (3 reads, 5 writes + action endpoints).
	mux.HandleFunc("POST /api/mail/accounts", s.handleMailAccountCreate)
	mux.HandleFunc("GET /api/mail/accounts", s.handleMailAccountList)
	mux.HandleFunc("GET /api/mail/accounts/{id}", s.handleMailAccountGet)
	mux.HandleFunc("PATCH /api/mail/accounts/{id}", s.handleMailAccountUpdate)
	mux.HandleFunc("DELETE /api/mail/accounts/{id}", s.handleMailAccountDelete)
	mux.HandleFunc("POST /api/mail/accounts/{id}/reset-password", s.handleMailAccountResetPassword)
	mux.HandleFunc("POST /api/mail/accounts/{id}/resync-imap", s.handleMailAccountResyncIMAP)
	mux.HandleFunc("POST /api/mail/accounts/{id}/disable", s.handleMailAccountDisable)

	// Aliases — 5 routes (2 reads, 3 writes).
	mux.HandleFunc("POST /api/mail/aliases", s.handleMailAliasCreate)
	mux.HandleFunc("GET /api/mail/aliases", s.handleMailAliasList)
	mux.HandleFunc("GET /api/mail/aliases/{id}", s.handleMailAliasGet)
	mux.HandleFunc("PATCH /api/mail/aliases/{id}", s.handleMailAliasUpdate)
	mux.HandleFunc("DELETE /api/mail/aliases/{id}", s.handleMailAliasDelete)

	// Import Registrations — 4 routes (2 reads, 2 writes + diagnostic probe).
	mux.HandleFunc("POST /api/mail/imports", s.handleMailImportRegister)
	mux.HandleFunc("GET /api/mail/imports", s.handleMailImportList)
	mux.HandleFunc("DELETE /api/mail/imports/{id}", s.handleMailImportDelete)
	mux.HandleFunc("POST /api/mail/imports/{id}/probe", s.handleMailImportProbe)

	// ---- Group 6: Delivery / Queue / Suppression / Webhooks / Outbound + DNSBL
	// Deliveries — 5 routes (2 reads, 3 writes).
	mux.HandleFunc("GET /api/mail/deliveries", s.handleMailDeliveryList)
	mux.HandleFunc("GET /api/mail/deliveries/{id}", s.handleMailDeliveryGet)
	mux.HandleFunc("POST /api/mail/deliveries/{id}/retry", s.handleMailDeliveryRetry)
	mux.HandleFunc("DELETE /api/mail/deliveries/{id}", s.handleMailDeliveryDelete)
	mux.HandleFunc("POST /api/mail/deliveries/prune", s.handleMailDeliveryPrune)

	// Queue — 3 routes (2 reads, 1 write).
	mux.HandleFunc("GET /api/mail/queue/summary", s.handleMailQueueGetSummary)
	mux.HandleFunc("GET /api/mail/queue/items", s.handleMailQueueList)
	mux.HandleFunc("POST /api/mail/queue/action/{action}", s.handleMailQueueBulkAction)

	// Suppressions — 5 routes (1 read, 4 writes).
	mux.HandleFunc("GET /api/mail/suppressions", s.handleMailSuppressionList)
	mux.HandleFunc("POST /api/mail/suppressions", s.handleMailSuppressionUpsert)
	mux.HandleFunc("DELETE /api/mail/suppressions/{id}", s.handleMailSuppressionDelete)
	mux.HandleFunc("POST /api/mail/suppressions/import", s.handleMailSuppressionBulkImport)
	mux.HandleFunc("POST /api/mail/suppressions/prune-expired", s.handleMailSuppressionPruneExpired)

	// Webhooks — 5 routes (2 reads, 3 writes).
	mux.HandleFunc("POST /api/mail/webhooks", s.handleMailWebhookRegister)
	mux.HandleFunc("GET /api/mail/webhooks", s.handleMailWebhookList)
	mux.HandleFunc("DELETE /api/mail/webhooks/{id}", s.handleMailWebhookDelete)
	mux.HandleFunc("POST /api/mail/webhooks/{id}/rotate-secret", s.handleMailWebhookRotateSecret)
	mux.HandleFunc("GET /api/mail/webhooks/events", s.handleMailWebhookEventList)

	// Webhook ingress — 1 route (no auth / no CSRF; guarded by CIDR + HMAC).
	mux.HandleFunc("POST /api/mail/hooks/in", s.handleMailWebhookIngest)

	// Outbound rate + thresholds — 3 routes (2 reads, 1 write).
	mux.HandleFunc("GET /api/mail/outbound/rate", s.handleMailOutboundRateGetSnapshot)
	mux.HandleFunc("GET /api/mail/outbound/thresholds", s.handleMailOutboundThresholdList)
	mux.HandleFunc("PATCH /api/mail/outbound/thresholds/{scope}", s.handleMailOutboundThresholdUpsert)

	// Reputation — 1 route (read-like, no write guards; long timeout).
	mux.HandleFunc("GET /api/mail/reputation/dnsbl", s.handleMailDNSBLProbeAll)

	// ---- Group 7: Mailbox (Folders + Messages + Search + IMAP Sync + Compose + Drafts)

	// Folders — 3 routes (1 read, 2 writes).
	mux.HandleFunc("GET /api/mail/accounts/{id}/folders", s.handleMailFolderList)
	mux.HandleFunc("POST /api/mail/accounts/{id}/folders", s.handleMailFolderUpsert)
	mux.HandleFunc("DELETE /api/mail/folders/{fid}", s.handleMailFolderDelete)

	// Messages — 7 routes (3 reads, 4 writes).
	mux.HandleFunc("GET /api/mail/folders/{fid}/messages", s.handleMailMessageList)
	mux.HandleFunc("GET /api/mail/messages/{mid}", s.handleMailMessageGet)
	mux.HandleFunc("DELETE /api/mail/messages/{mid}", s.handleMailMessageDelete)
	mux.HandleFunc("POST /api/mail/messages/{mid}/move", s.handleMailMessageMove)
	mux.HandleFunc("PATCH /api/mail/messages/{mid}/flags", s.handleMailMessageUpdateFlags)
	mux.HandleFunc("GET /api/mail/messages/{mid}/raw", s.handleMailMessageRaw)
	mux.HandleFunc("GET /api/mail/messages/{mid}/attachments/{idx}", s.handleMailMessageAttachment)

	// Search — 1 route (write-style so we can accept arbitrary-length JSON bodies).
	mux.HandleFunc("POST /api/mail/accounts/{id}/search", s.handleMailMessageSearch)

	// IMAP Index + Sync — 6 routes (2 reads, 4 writes).
	mux.HandleFunc("GET /api/mail/accounts/{id}/index/health", s.handleMailIndexHealthGet)
	mux.HandleFunc("GET /api/mail/index/health", s.handleMailIndexHealthList)
	mux.HandleFunc("POST /api/mail/accounts/{id}/index/reset", s.handleMailIndexHealthReset)
	mux.HandleFunc("POST /api/mail/accounts/{id}/sync/start", s.handleMailImapSyncStart)
	mux.HandleFunc("POST /api/mail/accounts/{id}/sync/pause", s.handleMailImapSyncPause)
	mux.HandleFunc("POST /api/mail/accounts/{id}/sync/resume", s.handleMailImapSyncResume)
	mux.HandleFunc("POST /api/mail/accounts/{id}/sync/reset", s.handleMailImapSyncReset)

	// Compose + Drafts — 3 routes (all writes; CSRF + drift + RO).
	mux.HandleFunc("POST /api/mail/compose/send", s.handleMailComposeSend)
	mux.HandleFunc("POST /api/mail/drafts", s.handleMailDraftSave)
	mux.HandleFunc("DELETE /api/mail/drafts/{did}", s.handleMailDraftDelete)

	// ---- Group 8: Logs + Backup + Retention + Danger Zone

	// Logs — 4 routes (all read-only, no CSRF).
	mux.HandleFunc("GET /api/mail/logs", s.handleMailLogsList)
	mux.HandleFunc("GET /api/mail/logs/files", s.handleMailLogsList)
	mux.HandleFunc("GET /api/mail/logs/tail", s.handleMailLogsTail)
	mux.HandleFunc("GET /api/mail/logs/stream", s.handleMailLogsStream)
	mux.HandleFunc("GET /api/mail/logs/redaction-summary", s.handleMailLogsRedactionSummary)
	mux.HandleFunc("GET /api/mail/logs/redaction", s.handleMailLogsRedactionSummary)

	// Backup — 7 routes (3 reads, 4 writes).
	mux.HandleFunc("GET /api/mail/backups", s.handleMailBackupList)
	mux.HandleFunc("POST /api/mail/backups", s.handleMailBackupCreate)
	mux.HandleFunc("GET /api/mail/backups/{bid}", s.handleMailBackupDownload)
	mux.HandleFunc("DELETE /api/mail/backups/{bid}", s.handleMailBackupDelete)
	mux.HandleFunc("GET /api/mail/backup/schedules", s.handleMailBackupScheduleList)
	mux.HandleFunc("POST /api/mail/backup/schedules", s.handleMailBackupScheduleUpsert)
	mux.HandleFunc("DELETE /api/mail/backup/schedules/{sid}", s.handleMailBackupScheduleDelete)
	mux.HandleFunc("GET /api/mail/backups/schedules", s.handleMailBackupScheduleList)
	mux.HandleFunc("POST /api/mail/backups/schedules", s.handleMailBackupScheduleUpsert)
	mux.HandleFunc("DELETE /api/mail/backups/schedules/{sid}", s.handleMailBackupScheduleDelete)

	// Retention — 4 routes (1 read, 3 writes).
	mux.HandleFunc("GET /api/mail/retention/rules", s.handleMailRetentionList)
	mux.HandleFunc("POST /api/mail/retention/rules", s.handleMailRetentionUpsert)
	mux.HandleFunc("DELETE /api/mail/retention/rules/{rid}", s.handleMailRetentionDelete)
	mux.HandleFunc("POST /api/mail/retention/apply-now", s.handleMailRetentionApplyNow)

	// Danger Zone — 3 routes (2 writes, 1 read).
	mux.HandleFunc("POST /api/mail/danger/generate-code", s.handleMailDangerGenerateCode)
	mux.HandleFunc("POST /api/mail/danger/hard-delete", s.handleMailDangerHardDelete)
	mux.HandleFunc("GET /api/mail/danger/requirements", s.handleMailDangerRequirements)
}

// MailStatusPayload is the Phase 1 status blob.  The struct is small now but
// it will grow in Phase 2 (observed state + 9 probe dots) and Phase 3
// (config_drifted flag).  Exposing `service_ready:bool` from day one means
// the UI can branch on it without a schema change.
type MailStatusPayload struct {
	OK                     bool                              `json:"ok"`
	ServiceReady           bool                              `json:"service_ready"`
	ConfigMode             string                            `json:"config_mode"`
	DesiredState           string                            `json:"desired_state"`
	PhantomInstance        string                            `json:"phantom_instance_id"`
	ImportMode             bool                              `json:"import_mode"`
	MoxRoot                string                            `json:"mox_root"`
	DomainCount            int                               `json:"domain_count"`
	AccountCount           int                               `json:"account_count"`
	EmergencyInboundReject *mail.EmergencyInboundRejectState `json:"emergency_inbound_reject,omitempty"`
}

// handleMailStatus returns the current Mail service status.  Phase 1: it
// answers from the settings row + service.IsRunning() so the UI can show a
// live "Module online" pill.  DomainCount/AccountCount are Phase 2 stubs
// returning 0 – we keep the keys so the UI dashboard summary layout doesn't
// need schema adjustment later.
//
// NOTE: /api/mail/status is intentionally UNPROTECTED so the login page can
// show a "mail system online" pill before the user authenticates.  It
// exposes no more information than the open-source install page already
// reveals (is a binary installed? is a process running?).
func (s *Server) handleMailStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ready := s.mail != nil && s.mail.IsRunning()
	settings, err := s.getMailSettingsCached(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, MailStatusPayload{
			OK:           false,
			ServiceReady: ready,
			MoxRoot:      safeMailMoxRoot(s.mail),
		})
		return
	}
	payload := MailStatusPayload{
		OK:              true,
		ServiceReady:    ready,
		ConfigMode:      settings.ConfigMode,
		DesiredState:    settings.DesiredState,
		PhantomInstance: settings.PhantomInstanceID,
		ImportMode:      settings.ImportMode,
		MoxRoot:         safeMailMoxRoot(s.mail),
		// Phases 2+ wire real counts from the store.
		DomainCount:  0,
		AccountCount: 0,
	}
	if s.mail != nil {
		if emergency, eerr := s.mail.EmergencyInboundRejectGet(ctx); eerr == nil {
			payload.EmergencyInboundReject = emergency
		}
	}
	writeJSON(w, http.StatusOK, payload)
}

// getMailSettingsCached is a tiny helper so handlers that need the singleton
// mail_mox_settings row don't each inline the same read.  The name is
// future-proofed for Phase 3 where the drift-guard snapshot will be attached
// to the request context.
func (s *Server) getMailSettingsCached(ctx context.Context) (*storage.MailMoxSettings, error) {
	if s.mail == nil {
		return nil, errMailNotWired
	}
	return s.store.MailGetSettings(ctx)
}

// safeMailMoxRoot returns s.mail.MoxRoot() if the service is wired, ""
// otherwise.  Keeps the HTTP layer free of nil-dereference surprises if
// someone (mis)constructs a Server without mail.
func safeMailMoxRoot(svc *mail.Service) string {
	if svc == nil {
		return ""
	}
	return svc.MoxRoot()
}

// errMailImportRO is a sentinel that writes HTTP handlers can return when
// the instance is in import read-only mode.  All destructive writes refuse
// with 403 + the same error code so the frontend can show a consistent
// "read-only" banner.
func writeMailImportROErr(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "import_read_only",
		"当前处于导入只读模式，操作被拒绝（如需变更请先退出导入模式）")
}

func writeMailCapabilityUnavailable(w http.ResponseWriter, err error) bool {
	if mail.ErrorCode(err) != "capability_unavailable" {
		return false
	}
	writeError(w, http.StatusNotImplemented, "capability_unavailable", err.Error())
	return true
}

// checkMailImportRO returns true and writes the 403 error if the service is
// in import mode.  Callers guard destructive writes with this helper.
func (s *Server) checkMailImportRO(w http.ResponseWriter, ctx context.Context) bool {
	settings, err := s.getMailSettingsCached(ctx)
	if err != nil || settings == nil {
		// Can't tell – be permissive; the underlying service has its own
		// guard and will refuse anyway.
		return false
	}
	if settings.ImportMode {
		writeMailImportROErr(w)
		return true
	}
	return false
}

// -----------------------------------------------------------------------------
// Binary handlers.  All are POST → require auth + CSRF.
// -----------------------------------------------------------------------------

// handleMailBinaryDetect — POST /api/mail/binary/detect
func (s *Server) handleMailBinaryDetect(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	var req mail.BinaryDetectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.mail.BinaryDetect(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "binary_detect_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailBinaryDownload — POST /api/mail/binary/download
func (s *Server) handleMailBinaryDownload(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	var req mail.BinaryDownloadRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.mail.BinaryDownload(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "binary_download_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailBinaryInstall — POST /api/mail/binary/install
func (s *Server) handleMailBinaryInstall(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	var req mail.BinaryInstallRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.mail.BinaryInstall(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "binary_install_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailBinaryUninstall — POST /api/mail/binary/uninstall
func (s *Server) handleMailBinaryUninstall(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	var req mail.BinaryUninstallRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.mail.BinaryUninstall(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "binary_uninstall_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// -----------------------------------------------------------------------------
// Setup handlers.  POST → require auth + CSRF.
// -----------------------------------------------------------------------------

// handleMailSetupInitialize — POST /api/mail/setup/initialize
func (s *Server) handleMailSetupInitialize(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	var req mail.SetupInitializeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.mail.SetupInitialize(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "setup_initialize_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailSetupImport — POST /api/mail/setup/import
//
// The import handler is deliberately UNGUARDED by checkMailImportRO: switching
// INTO import mode must always be allowed (an operator that imported once
// should be able to re-import a different external Mox without flipping state
// in between).
func (s *Server) handleMailSetupImport(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var req mail.SetupImportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.mail.SetupImport(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "setup_import_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailSetupPreflightPorts — POST /api/mail/setup/preflight-ports
// (non-destructive, but we still require auth+CSRF because POST requests
// are protected by the XHR cross-origin policy; any unprotected endpoint
// would be an unnecessary regression in defence-in-depth).
func (s *Server) handleMailSetupPreflightPorts(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	var req mail.PreflightPortsRequest
	// Body is optional – empty request runs the default port set.
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	resp, err := s.mail.PreflightPorts(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "preflight_ports_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// -----------------------------------------------------------------------------
// Runtime lifecycle handlers.
// -----------------------------------------------------------------------------

// handleMailRuntimeStatus — GET /api/mail/runtime/status
// (read-only; auth required because the full status exposes PID + install
// paths that /api/mail/status intentionally hides).
func (s *Server) handleMailRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	resp, err := s.mail.RuntimeStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "runtime_status_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailRuntimeStart — POST /api/mail/runtime/start
func (s *Server) handleMailRuntimeStart(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	var req mail.LifecycleRequest
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	resp, err := s.mail.Start(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "start_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailRuntimeStop — POST /api/mail/runtime/stop
func (s *Server) handleMailRuntimeStop(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	var req mail.LifecycleRequest
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	resp, err := s.mail.Stop(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "stop_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailRuntimeRestart — POST /api/mail/runtime/restart
func (s *Server) handleMailRuntimeRestart(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	var req mail.LifecycleRequest
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	resp, err := s.mail.Restart(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "restart_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailRuntimeProbe — POST /api/mail/runtime/probe
func (s *Server) handleMailRuntimeProbe(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	var req mail.RuntimeProbeRequest
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	resp, err := s.mail.RuntimeProbe(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "runtime_probe_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// -----------------------------------------------------------------------------
// Phase 3 helpers: drift guard.
// -----------------------------------------------------------------------------

// checkMailDrift returns true + writes HTTP 409 if the on-disk mox.conf has
// diverged from the last Phantom-synced version.  The intent is to reject
// writes until the operator resolves drift via /runtime/resolve-drift.
//
// Read-only GET endpoints may skip this check; every destructive write
// handler (except resolve-drift itself) MUST call it.
func (s *Server) checkMailDrift(w http.ResponseWriter, ctx context.Context) bool {
	if s.mail == nil {
		return false
	}
	if s.mail.Drifted() {
		writeError(w, http.StatusConflict, "config_drifted",
			"on-disk mox.conf 已与 Phantom 记录的版本发生偏离，请先通过 POST /api/mail/runtime/resolve-drift 确认处理策略 (overwrite 或 reimport)")
		return true
	}
	return false
}

// -----------------------------------------------------------------------------
// Phase 3: Config application + drift handlers.
// -----------------------------------------------------------------------------

// handleMailConfigValidate — POST /api/mail/config/validate
func (s *Server) handleMailConfigValidate(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	var req mail.ConfigValidateRequest
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	resp, err := s.mail.ConfigValidate(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "validate_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailConfigApply — POST /api/mail/config/apply
// Runs the 10-step pipeline synchronously and returns the result.
func (s *Server) handleMailConfigApply(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var req mail.ConfigApplyRequest
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	resp, err := s.mail.ConfigApply(r.Context(), req, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_apply_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailConfigApplyStream — POST /api/mail/config/apply/stream
// Runs the 10-step pipeline and streams StepStatus events as SSE.
func (s *Server) handleMailConfigApplyStream(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var req mail.ConfigApplyRequest
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	progress := make(chan configapply.StepStatus, 16)
	go func() {
		resp, err := s.mail.ConfigApply(r.Context(), req, progress)
		_ = err
		// Emit final result event as synthetic "final" step after progress closes.
		if resp != nil {
			progress <- configapply.StepStatus{
				Step: 0, Total: 10, Name: "Final",
				Percent: 100, State: "done", Message: resp.Summary,
			}
		}
		close(progress)
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	seq := int64(0)
	if !ok {
		// Fallback: collect and emit one JSON blob.
		var all []mailConfigStepAlias
		for ev := range progress {
			all = append(all, ev)
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": all})
		return
	}
	for ev := range progress {
		seq++
		buf, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "id: %d\n", seq)
		fmt.Fprintf(w, "event: step\n")
		fmt.Fprintf(w, "data: %s\n\n", buf)
		flusher.Flush()
	}
	fmt.Fprintf(w, "event: done\n")
	fmt.Fprintf(w, "data: {}\n\n")
	flusher.Flush()
}

type mailConfigStepAlias = struct {
	Step    int    `json:"step"`
	Total   int    `json:"total"`
	Name    string `json:"name"`
	Percent int    `json:"percent"`
	Message string `json:"message,omitempty"`
	Output  string `json:"output,omitempty"`
	State   string `json:"state"`
}
type configapplyStepAlias = mailConfigStepAlias

// handleMailConfigRollback — POST /api/mail/config/rollback
func (s *Server) handleMailConfigRollback(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	var req mail.ConfigRollbackRequest
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	resp, err := s.mail.ConfigRollback(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "rollback_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailConfigSummary — GET /api/mail/config/summary
func (s *Server) handleMailConfigSummary(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	resp, err := s.mail.ConfigSummary(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_summary_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailResolveDrift — POST /api/mail/config/resolve-drift
func (s *Server) handleMailResolveDrift(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	var req mail.ResolveDriftRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.mail.ResolveDrift(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "resolve_drift_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// keep events import used (writeSSE uses events.Event elsewhere)
var _ = events.Event{}.Type
var _ = time.Second

// -----------------------------------------------------------------------------
// Phase 3: Domain handlers.
// -----------------------------------------------------------------------------

// handleMailDomainList — GET /api/mail/domains
func (s *Server) handleMailDomainList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	list, err := s.mail.DomainList(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "domain_list_failed", err.Error())
		return
	}
	if list == nil {
		list = []*storage.MailDomain{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":   list,
		"count":   len(list),
		"drifted": s.mail.Drifted(),
	})
}

// handleMailDomainCreate — POST /api/mail/domains
func (s *Server) handleMailDomainCreate(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var req storage.MailDomain
	if !decodeJSON(w, r, &req) {
		return
	}
	created, err := s.mail.DomainCreate(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "domain_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// handleMailDomainGet — GET /api/mail/domains/{id}
func (s *Server) handleMailDomainGet(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	id := r.PathValue("id")
	domains, err := s.mail.DomainList(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "domain_get_failed", err.Error())
		return
	}
	for _, d := range domains {
		if d != nil && d.ID == id {
			writeJSON(w, http.StatusOK, d)
			return
		}
	}
	writeError(w, http.StatusNotFound, "domain_not_found", "domain "+id+" not found")
}

// handleMailDomainUpdate — PUT /api/mail/domains/{id}
func (s *Server) handleMailDomainUpdate(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	var req storage.MailDomain
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ID == "" {
		req.ID = id
	}
	updated, err := s.mail.DomainUpdate(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "domain_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleMailDomainDelete — DELETE /api/mail/domains/{id}
func (s *Server) handleMailDomainDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	if err := s.mail.DomainDelete(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, "domain_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

// handleMailDomainEnable — POST /api/mail/domains/{id}/enable
// Body: {"enable": bool}
func (s *Server) handleMailDomainEnable(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	var body struct {
		Enable bool `json:"enable"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	updated, err := s.mail.DomainEnable(r.Context(), id, body.Enable)
	if err != nil {
		writeError(w, http.StatusBadRequest, "domain_enable_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleMailDomainDisable — POST /api/mail/domains/{id}/disable
func (s *Server) handleMailDomainDisable(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	updated, err := s.mail.DomainEnable(r.Context(), id, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "domain_disable_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleMailDomainDNSCheck — POST /api/mail/domains/{id}/dns-check
func (s *Server) handleMailDomainDNSCheck(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	// Read-only-ish (writes result JSON) — no drift check since it's diagnostic.
	id := r.PathValue("id")
	updated, err := s.mail.DomainDNSCheck(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "domain_dns_check_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"domain":     updated,
		"dns_status": mail.DomainDNSStatusFromCheckJSON(updated.DNSCheckJSON),
		"dns_check":  parseMailDNSCheckJSON(updated.DNSCheckJSON),
	})
}

func parseMailDNSCheckJSON(raw string) any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	return payload
}

// handleMailEmergencyInboundRejectGet — GET /api/mail/emergency/inbound-reject
func (s *Server) handleMailEmergencyInboundRejectGet(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	state, err := s.mail.EmergencyInboundRejectGet(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "emergency_get_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleMailEmergencyInboundRejectEnable(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	var req mail.EmergencyInboundRejectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	state, pipeline, err := s.mail.EmergencyInboundRejectEnable(r.Context(), req)
	if err != nil {
		if writeMailEmergencyCodedError(w, err, state, pipeline) {
			return
		}
		writeMailEmergencyError(w, http.StatusBadRequest, "emergency_enable_failed", err.Error(), state, pipeline)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": state, "pipeline": pipeline})
}

func (s *Server) handleMailEmergencyInboundRejectDisable(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	var req mail.EmergencyInboundRejectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	state, pipeline, err := s.mail.EmergencyInboundRejectDisable(r.Context(), req)
	if err != nil {
		if writeMailEmergencyCodedError(w, err, state, pipeline) {
			return
		}
		writeMailEmergencyError(w, http.StatusBadRequest, "emergency_disable_failed", err.Error(), state, pipeline)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": state, "pipeline": pipeline})
}

func writeMailEmergencyCodedError(w http.ResponseWriter, err error, state *mail.EmergencyInboundRejectState, pipeline *configapply.PipelineResult) bool {
	switch code := mail.ErrorCode(err); code {
	case "config_drifted":
		writeMailEmergencyError(w, http.StatusConflict, code, err.Error(), state, pipeline)
		return true
	case "import_read_only":
		writeMailEmergencyError(w, http.StatusForbidden, code, err.Error(), state, pipeline)
		return true
	default:
		return false
	}
}

func writeMailEmergencyError(w http.ResponseWriter, status int, code, message string, state *mail.EmergencyInboundRejectState, pipeline *configapply.PipelineResult) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
		"state":    state,
		"pipeline": pipeline,
	})
}

// handleMailDomainDNSRecords — GET /api/mail/domains/{id}/dns-records
func (s *Server) handleMailDomainDNSRecords(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	id := r.PathValue("id")
	recs, err := s.mail.DomainDNSRecords(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "domain_dns_records_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"domain_id": id,
		"records":   recs,
	})
}

// ============================================================================
// Group 4 — CertManager (Phase 4)
// ============================================================================
//
// Twelve routes split across three sub-groups:
//   (A) /api/mail/certificates      — list / issue / get / renew / rollback / delete
//   (B) /api/mail/dns-providers     — list / upsert / delete / test
//   (C) /api/mail/manual-challenges — list / confirm / cancel
//
// Writes go through auth+CSRF+import_RO+drift; reads only need auth.
// ============================================================================

// --- (A) Certificates ------------------------------------------------------

// handleMailCertificateList — GET /api/mail/certificates
func (s *Server) handleMailCertificateList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	resp, err := s.mail.MailCertificateList(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cert_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailCertificateIssue — POST /api/mail/certificates
func (s *Server) handleMailCertificateIssue(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var req mail.CertIssueRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.mail.MailCertificateIssue(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cert_issue_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailCertificateGet — GET /api/mail/certificates/{id}
func (s *Server) handleMailCertificateGet(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	id := r.PathValue("id")
	resp, err := s.mail.MailCertificateGet(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "cert_not_found", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailCertificateRenew — POST /api/mail/certificates/{id}/renew
func (s *Server) handleMailCertificateRenew(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	var body struct {
		Force bool `json:"force"`
	}
	_ = decodeJSON(w, r, &body) // optional body; default force=false
	resp, err := s.mail.MailCertificateRenew(r.Context(), id, body.Force)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cert_renew_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailCertificateRollback — POST /api/mail/certificates/{id}/rollback
func (s *Server) handleMailCertificateRollback(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	resp, err := s.mail.MailCertificateRollback(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cert_rollback_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailCertificateDelete — DELETE /api/mail/certificates/{id}
func (s *Server) handleMailCertificateDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	resp, err := s.mail.MailCertificateDelete(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cert_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- (B) DNS Providers ----------------------------------------------------

// handleMailDNSProviderList — GET /api/mail/dns-providers
func (s *Server) handleMailDNSProviderList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	providers, err := s.mail.MailDNSProviderList(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "dns_provider_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": providers,
		"count": len(providers),
	})
}

// handleMailDNSProviderUpsert — POST /api/mail/dns-providers
func (s *Server) handleMailDNSProviderUpsert(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var req mail.DNSProviderUpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.mail.MailDNSProviderUpsert(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "dns_provider_upsert_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailDNSProviderDelete — DELETE /api/mail/dns-providers/{id}
func (s *Server) handleMailDNSProviderDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	if err := s.mail.MailDNSProviderDelete(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, "dns_provider_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleMailDNSProviderTest — POST /api/mail/dns-providers/{id}/test
func (s *Server) handleMailDNSProviderTest(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	// Test is "read-ish" but has a network side-effect; skip drift guard so
	// operators can still run diagnostics while drift is outstanding.
	id := r.PathValue("id")
	resp, err := s.mail.MailDNSProviderTest(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "dns_provider_test_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- (C) Manual Challenges ------------------------------------------------

// handleMailManualChallengeList — GET /api/mail/manual-challenges
func (s *Server) handleMailManualChallengeList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	resp, err := s.mail.MailManualChallengeList(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "manual_challenge_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailManualChallengeConfirm — POST /api/mail/manual-challenges/{id}/confirm
func (s *Server) handleMailManualChallengeConfirm(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	resp, err := s.mail.MailManualChallengeConfirm(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "manual_challenge_confirm_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailManualChallengeCancel — DELETE /api/mail/manual-challenges/{id}
func (s *Server) handleMailManualChallengeCancel(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	resp, err := s.mail.MailManualChallengeCancel(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "manual_challenge_cancel_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailAccountCreate — POST /api/mail/accounts
func (s *Server) handleMailAccountCreate(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var req mail.AccountCreateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	created, err := s.mail.MailAccountCreate(r.Context(), req)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "account_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":                created.Account.ID,
		"address":           created.Account.Address,
		"display_name":      created.Account.DisplayName,
		"one_time_password": created.GeneratedPassword,
		"created_at":        created.Account.CreatedAt,
	})
}

// handleMailAccountList — GET /api/mail/accounts?domain_id=&status=
func (s *Server) handleMailAccountList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	domainID := r.URL.Query().Get("domain_id")
	status := r.URL.Query().Get("status")
	list, err := s.mail.MailAccountList(r.Context(), domainID, status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleMailAccountGet — GET /api/mail/accounts/{id}
func (s *Server) handleMailAccountGet(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	id := r.PathValue("id")
	got, err := s.mail.MailAccountGet(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account_not_found", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "account_get_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// handleMailAccountUpdate — PATCH /api/mail/accounts/{id}
func (s *Server) handleMailAccountUpdate(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	var req mail.AccountUpdateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ID == "" {
		req.ID = id
	}
	updated, err := s.mail.MailAccountUpdate(r.Context(), req)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "account_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleMailAccountDelete — DELETE /api/mail/accounts/{id}
func (s *Server) handleMailAccountDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	if err := s.mail.MailAccountDelete(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "account_delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMailAccountResetPassword — POST /api/mail/accounts/{id}/reset-password
func (s *Server) handleMailAccountResetPassword(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	resp, err := s.mail.MailAccountResetPassword(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "account_reset_password_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailAccountResyncIMAP — POST /api/mail/accounts/{id}/resync-imap
func (s *Server) handleMailAccountResyncIMAP(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	updated, err := s.mail.MailAccountResyncIMAP(r.Context(), id)
	if err != nil {
		if writeMailCapabilityUnavailable(w, err) {
			return
		}
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "account_resync_imap_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleMailAccountDisable — POST /api/mail/accounts/{id}/disable
func (s *Server) handleMailAccountDisable(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	updated, err := s.mail.MailAccountDisable(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "account_disable_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// ---- Group 5 Aliases ---------------------------------------------------------

// handleMailAliasCreate — POST /api/mail/aliases
func (s *Server) handleMailAliasCreate(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var req mail.AliasUpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ID = "" // force create
	created, err := s.mail.MailAliasUpsert(r.Context(), req)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "alias_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// handleMailAliasList — GET /api/mail/aliases?domain_id=&mode=
func (s *Server) handleMailAliasList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	domainID := r.URL.Query().Get("domain_id")
	mode := r.URL.Query().Get("mode")
	list, err := s.mail.MailAliasList(r.Context(), domainID, mode)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "alias_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleMailAliasGet — GET /api/mail/aliases/{id}
func (s *Server) handleMailAliasGet(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	id := r.PathValue("id")
	got, err := s.mail.MailAliasGet(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alias_not_found", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "alias_get_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// handleMailAliasUpdate — PATCH /api/mail/aliases/{id}
func (s *Server) handleMailAliasUpdate(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	var req mail.AliasUpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ID = id // force update
	updated, err := s.mail.MailAliasUpsert(r.Context(), req)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alias_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "alias_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleMailAliasDelete — DELETE /api/mail/aliases/{id}
func (s *Server) handleMailAliasDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	if err := s.mail.MailAliasDelete(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alias_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "alias_delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Group 5 Import ----------------------------------------------------------

// handleMailImportRegister — POST /api/mail/imports
func (s *Server) handleMailImportRegister(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	// NOTE: import register intentionally bypasses checkMailImportRO (the
	// whole point is to transition INTO import mode).  It still respects
	// config_drifted.
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var req mail.ImportRegisterRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	created, err := s.mail.MailImportRegister(r.Context(), req)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "import_register_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// handleMailImportList — GET /api/mail/imports
func (s *Server) handleMailImportList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	list, err := s.mail.MailImportList(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "import_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleMailImportDelete — DELETE /api/mail/imports/{id}
func (s *Server) handleMailImportDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	// NOTE: import delete transitions OUT of import mode when it was the last
	// registration; it should NOT be blocked by checkMailImportRO.  It still
	// respects config_drifted.
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	if err := s.mail.MailImportDelete(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "import_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "import_delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMailImportProbe — POST /api/mail/imports/{id}/probe
func (s *Server) handleMailImportProbe(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	// NOTE: probe is diagnostic; NOT gated by checkMailImportRO so the
	// operator can verify an import source even after transitioning into
	// import mode.  Still gated by config_drifted because a drifted install
	// cannot be reliably queried.
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	resp, err := s.mail.MailImportProbe(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "import_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "import_probe_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- Group 6 Deliveries ------------------------------------------------------

// handleMailDeliveryList — GET /api/mail/deliveries
// Paginated list of delivery events.  Read-only: no CSRF / drift / RO guards.
func (s *Server) handleMailDeliveryList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	f := storage.MailDeliveryListFilter{
		Status:     r.URL.Query().Get("status"),
		Direction:  r.URL.Query().Get("direction"),
		FromDomain: r.URL.Query().Get("from_domain"),
		ToDomain:   r.URL.Query().Get("to_domain"),
		Search:     r.URL.Query().Get("search"),
		Limit:      parseInt(r.URL.Query().Get("limit")),
		Cursor:     r.URL.Query().Get("cursor"),
	}
	_ = f
	resp, err := s.mail.DeliveryList(r.Context(), storage.MailDeliveryListFilter{
		Status:     r.URL.Query().Get("status"),
		Direction:  r.URL.Query().Get("direction"),
		FromDomain: r.URL.Query().Get("from_domain"),
		ToDomain:   r.URL.Query().Get("to_domain"),
		Search:     r.URL.Query().Get("search"),
		Limit:      parseInt(r.URL.Query().Get("limit")),
		Cursor:     r.URL.Query().Get("cursor"),
	})
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "delivery_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailDeliveryGet — GET /api/mail/deliveries/{id}
func (s *Server) handleMailDeliveryGet(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	id := r.PathValue("id")
	item, err := s.mail.DeliveryGet(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "delivery_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "delivery_get_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// handleMailDeliveryRetry — POST /api/mail/deliveries/{id}/retry
func (s *Server) handleMailDeliveryRetry(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	if err := s.mail.DeliveryRetry(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "delivery_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "delivery_retry_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "retried": true})
}

// handleMailDeliveryDelete — DELETE /api/mail/deliveries/{id}
func (s *Server) handleMailDeliveryDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	if err := s.mail.DeliveryDelete(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "delivery_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "delivery_delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMailDeliveryPrune — POST /api/mail/deliveries/prune
// Body: {days?: number} default 90 → {pruned_count: N}
func (s *Server) handleMailDeliveryPrune(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	type pruneBody struct {
		Days *int `json:"days"`
	}
	b := pruneBody{}
	if !decodeJSON(w, r, &b) {
		return
	}
	days := 90
	if b.Days != nil && *b.Days > 0 {
		days = *b.Days
	}
	n, err := s.mail.DeliveryPrune(r.Context(), days)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "delivery_prune_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pruned_count": n})
}

// ---- Group 6 Queue ---------------------------------------------------------

// handleMailQueueGetSummary — GET /api/mail/queue/summary
func (s *Server) handleMailQueueGetSummary(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	summary, err := s.mail.QueueGetSummary(r.Context())
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "queue_summary_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// handleMailQueueList — GET /api/mail/queue/items
// Query: bucket, limit, cursor
func (s *Server) handleMailQueueList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	bucket := r.URL.Query().Get("bucket")
	cursor := r.URL.Query().Get("cursor")
	limit := parseInt(r.URL.Query().Get("limit"))
	items, err := s.mail.QueueList(r.Context(), bucket, cursor, limit)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "queue_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleMailQueueBulkAction — POST /api/mail/queue/action/{action}
// action ∈ {hold, unhold, schedule, fail, drop}
// Body: {ids: []string} → {updated: N}
func (s *Server) handleMailQueueBulkAction(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	action := r.PathValue("action")
	switch action {
	case "hold", "unhold", "schedule", "fail", "drop":
	default:
		writeError(w, http.StatusBadRequest, "invalid_queue_action", fmt.Sprintf("unknown queue action %q", action))
		return
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids_required", "ids list is required")
		return
	}
	n, err := s.mail.QueueBulkAction(r.Context(), body.IDs, action)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "queue_action_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": n})
}

// ---- Group 6 Suppressions --------------------------------------------------

// handleMailSuppressionList — GET /api/mail/suppressions
// Query: active (1/true), reason, domain_id, search, cursor, limit
func (s *Server) handleMailSuppressionList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	f := storage.MailSuppressionFilter{
		Reason:   r.URL.Query().Get("reason"),
		DomainID: r.URL.Query().Get("domain_id"),
		Search:   r.URL.Query().Get("search"),
		Cursor:   r.URL.Query().Get("cursor"),
		Limit:    parseInt(r.URL.Query().Get("limit")),
	}
	if raw := r.URL.Query().Get("active"); raw != "" {
		rawLower := strings.ToLower(strings.TrimSpace(raw))
		if rawLower == "1" || rawLower == "true" {
			b := true
			f.Active = &b
		} else if rawLower == "0" || rawLower == "false" {
			b := false
			f.Active = &b
		}
	}
	items, err := s.mail.SuppressionList(r.Context(), f)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "suppression_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleMailSuppressionUpsert — POST /api/mail/suppressions
func (s *Server) handleMailSuppressionUpsert(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var in storage.MailSuppression
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := s.mail.SuppressionUpsert(r.Context(), &in)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "suppression_upsert_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleMailSuppressionDelete — DELETE /api/mail/suppressions/{id}
func (s *Server) handleMailSuppressionDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	if err := s.mail.SuppressionDelete(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "suppression_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "suppression_delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMailSuppressionBulkImport — POST /api/mail/suppressions/import
// Body: {entries: [{recipient_hash, reason, source?, smtp_code?, expires_at?}]}
// Resp: {imported_count: N}
func (s *Server) handleMailSuppressionBulkImport(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var body struct {
		Entries []struct {
			RecipientHash string `json:"recipient_hash"`
			Reason        string `json:"reason"`
			Source        string `json:"source"`
			SMTPCode      int    `json:"smtp_code"`
			ExpiresAt     string `json:"expires_at"`
		} `json:"entries"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Entries) == 0 {
		writeError(w, http.StatusBadRequest, "entries_required", "entries list is required")
		return
	}
	entries := make([]storage.MailSuppression, 0, len(body.Entries))
	for _, e := range body.Entries {
		entries = append(entries, storage.MailSuppression{
			RecipientHash: e.RecipientHash,
			Reason:        e.Reason,
			Source:        e.Source,
			SMTPCode:      e.SMTPCode,
			ExpiresAt:     e.ExpiresAt,
		})
	}
	n, err := s.mail.SuppressionBulkImport(r.Context(), entries)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "suppression_import_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported_count": n})
}

// handleMailSuppressionPruneExpired — POST /api/mail/suppressions/prune-expired
func (s *Server) handleMailSuppressionPruneExpired(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	n, err := s.mail.SuppressionPruneExpired(r.Context())
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "suppression_prune_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pruned_count": n})
}

// ---- Group 6 Webhooks OUT ------------------------------------------------

// handleMailWebhookRegister — POST /api/mail/webhooks
// Resp: {registration, one_time_secret}
func (s *Server) handleMailWebhookRegister(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var req mail.WebhookRegisterRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	reg, secret, err := s.mail.WebhookRegister(r.Context(), &req)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "webhook_register_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"registration":    reg,
		"one_time_secret": secret,
	})
}

// handleMailWebhookList — GET /api/mail/webhooks
func (s *Server) handleMailWebhookList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	list, err := s.mail.WebhookList(r.Context())
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "webhook_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// handleMailWebhookDelete — DELETE /api/mail/webhooks/{id}
func (s *Server) handleMailWebhookDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	if err := s.mail.WebhookDelete(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "webhook_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "webhook_delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMailWebhookRotateSecret — POST /api/mail/webhooks/{id}/rotate-secret
func (s *Server) handleMailWebhookRotateSecret(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	id := r.PathValue("id")
	secret, err := s.mail.WebhookRotateSecret(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "webhook_not_found", err.Error())
			return
		}
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "webhook_rotate_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"registration_id": id,
		"one_time_secret": secret,
	})
}

// handleMailWebhookEventList — GET /api/mail/webhooks/events
func (s *Server) handleMailWebhookEventList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	limit := parseInt(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	list, err := s.mail.WebhookEventList(r.Context(), limit)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "webhook_events_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// handleMailWebhookIngest — POST /api/mail/hooks/in
// No auth / no CSRF.  Validated by HMAC + CIDR inside the service.
// Errors mapped to stable 4xx; never 5xx.
func (s *Server) handleMailWebhookIngest(w http.ResponseWriter, r *http.Request) {
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	timestampStr := r.Header.Get("X-Mox-Timestamp")
	signatureHeader := r.Header.Get("X-Mox-Signature")
	remote := r.RemoteAddr
	if timestampStr == "" {
		writeError(w, http.StatusBadRequest, "bad_timestamp", "missing X-Mox-Timestamp header")
		return
	}
	if signatureHeader == "" {
		writeError(w, http.StatusUnauthorized, "signature_missing", "missing X-Mox-Signature header")
		return
	}
	// Cap read at 1 MiB — we don't trust upstream to be bounded.
	const maxBody = 1 << 20
	lr := io.LimitReader(r.Body, maxBody+1)
	body, rerr := io.ReadAll(lr)
	_ = r.Body.Close()
	if rerr != nil {
		writeError(w, http.StatusBadRequest, "body_read_failed", rerr.Error())
		return
	}
	if int64(len(body)) > maxBody {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "payload exceeds max allowed size")
		return
	}
	status, eventID, err := s.mail.WebhookIngest(r.Context(), remote, timestampStr, signatureHeader, body)
	if err != nil {
		msg := err.Error()
		lower := strings.ToLower(msg)
		switch {
		case strings.Contains(lower, "timestamp"):
			writeError(w, http.StatusBadRequest, "bad_timestamp", msg)
		case strings.Contains(lower, "hmac"), strings.Contains(lower, "signature"):
			writeError(w, http.StatusUnauthorized, "hmac_invalid", msg)
		case strings.Contains(lower, "source"), strings.Contains(lower, "blocked"), strings.Contains(lower, "no registration"):
			writeError(w, http.StatusForbidden, "source_blocked", msg)
		default:
			// Catch-all: never leak 5xx to upstream
			writeError(w, http.StatusBadRequest, "ingress_rejected", msg)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "event_id": eventID})
}

// ---- Group 6 Outbound Rate + Thresholds -----------------------------------

// handleMailOutboundRateGetSnapshot — GET /api/mail/outbound/rate
// Query: scope (default "global")
func (s *Server) handleMailOutboundRateGetSnapshot(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "global"
	}
	snap, err := s.mail.OutboundRateGetSnapshot(r.Context(), scope)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "rate_snapshot_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// handleMailOutboundThresholdList — GET /api/mail/outbound/thresholds
func (s *Server) handleMailOutboundThresholdList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	list, err := s.mail.OutboundThresholdList(r.Context())
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "threshold_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// handleMailOutboundThresholdUpsert — PATCH /api/mail/outbound/thresholds/{scope}
func (s *Server) handleMailOutboundThresholdUpsert(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	scope := r.PathValue("scope")
	var in storage.MailOutboundThreshold
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Scope = scope
	out, err := s.mail.OutboundThresholdUpsert(r.Context(), &in)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if code == "import_read_only" {
			writeError(w, http.StatusForbidden, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "threshold_upsert_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- Group 6 Reputation DNSBL -----------------------------------------

// handleMailDNSBLProbeAll — GET /api/mail/reputation/dnsbl
// Read-like; no write guards.  Sets a 30s deadline for the probe.
func (s *Server) handleMailDNSBLProbeAll(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.mail.DNSBLProbeAll(ctx)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "config_drifted" {
			writeError(w, http.StatusConflict, code, err.Error())
			return
		}
		if ctx.Err() != nil {
			writeError(w, http.StatusGatewayTimeout, "dnsbl_timeout", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "dnsbl_probe_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// keep import packages in use (some groups only use them conditionally)
var _ context.Context
var _ json.RawMessage
var _ = fmt.Sprintf
var _ = time.Second
var _ storage.Session
var _ = bytes.Equal
var _ = strconv.Itoa
var _ = strings.ToLower
var _ = io.EOF
var _ events.Event
var _ = io.LimitReader

// ---- Group 7: Mailbox (Folders + Messages + Search + IMAP Sync + Compose + Drafts)

// handleMailFolderList — GET /api/mail/accounts/{id}/folders
// Read-only: no CSRF, no drift, no RO check.
func (s *Server) handleMailFolderList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	accountID := r.PathValue("id")
	// Allow "" when accountID == "all" so a future admin view can fetch every
	// folder on the instance; the service layer tolerates empty string.
	if accountID == "all" {
		accountID = ""
	}
	out, err := s.mail.MailFolderList(r.Context(), accountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "folder_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// handleMailFolderUpsert — POST /api/mail/accounts/{id}/folders
func (s *Server) handleMailFolderUpsert(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	accountID := r.PathValue("id")
	var req storage.MailFolder
	if !decodeJSON(w, r, &req) {
		return
	}
	req.AccountID = accountID
	resp, err := s.mail.MailFolderUpsert(r.Context(), req)
	if err != nil {
		if writeMailCapabilityUnavailable(w, err) {
			return
		}
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "folder_not_found", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "folder_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailFolderDelete — DELETE /api/mail/folders/{fid}
func (s *Server) handleMailFolderDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	folderID := r.PathValue("fid")
	if err := s.mail.MailFolderDelete(r.Context(), folderID); err != nil {
		if writeMailCapabilityUnavailable(w, err) {
			return
		}
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "folder_not_found", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "folder_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMailMessageList — GET /api/mail/folders/{fid}/messages
func (s *Server) handleMailMessageList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	limit := parseInt(r.URL.Query().Get("limit"))
	cursor := r.URL.Query().Get("cursor")
	unseenOnly := r.URL.Query().Get("unseen_only") == "1"
	resp, err := s.mail.MailMessageList(r.Context(), mail.MailMessageListFilter{
		FolderID:   r.PathValue("fid"),
		Limit:      limit,
		Cursor:     cursor,
		UnseenOnly: unseenOnly,
	})
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "folder_not_found", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "message_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailMessageGet — GET /api/mail/messages/{mid}
func (s *Server) handleMailMessageGet(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	messageID := r.PathValue("mid")
	detail, err := s.mail.MailMessageGet(r.Context(), messageID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "message_not_found", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "message_get_failed", err.Error())
		return
	}
	// Hydrate attachment metadata (index, filename, size) from the part list.
	var atts []mail.AttachmentInfo
	idx := 0
	for _, p := range detail.Parts {
		if !p.IsAttachment {
			continue
		}
		atts = append(atts, mail.AttachmentInfo{
			Index:       idx,
			PartID:      p.ID,
			Filename:    p.Filename,
			ContentType: p.ContentType,
			SizeBytes:   p.SizeBytes,
			Stored:      p.BodyCachePath != "",
		})
		idx++
	}
	if len(detail.Attachments) == 0 {
		detail.Attachments = atts
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleMailMessageDelete — DELETE /api/mail/messages/{mid}
// HIGH destructive: service emits danger=true in the event payload.
func (s *Server) handleMailMessageDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	messageID := r.PathValue("mid")
	if err := s.mail.MailMessageDelete(r.Context(), messageID); err != nil {
		if writeMailCapabilityUnavailable(w, err) {
			return
		}
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "message_not_found", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "message_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMailMessageMove — POST /api/mail/messages/{mid}/move
func (s *Server) handleMailMessageMove(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	messageID := r.PathValue("mid")
	var body struct {
		DestFolderID string `json:"dest_folder_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.mail.MailMessageMove(r.Context(), messageID, body.DestFolderID); err != nil {
		if writeMailCapabilityUnavailable(w, err) {
			return
		}
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "message_not_found", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "message_move_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMailMessageUpdateFlags — PATCH /api/mail/messages/{mid}/flags
func (s *Server) handleMailMessageUpdateFlags(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	messageID := r.PathValue("mid")
	var body struct {
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.mail.MailMessageFlagsUpdate(r.Context(), messageID, body.Add, body.Remove); err != nil {
		if writeMailCapabilityUnavailable(w, err) {
			return
		}
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "message_not_found", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "flags_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMailMessageRaw — GET /api/mail/messages/{mid}/raw
// Returns ONLY the decoded text/plain body as Content-Type: text/plain — no MIME,
// no attachments, no envelope headers.  Intentionally small surface area.
func (s *Server) handleMailMessageRaw(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	messageID := r.PathValue("mid")
	text, err := s.mail.MailMessageRaw(r.Context(), messageID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "message_not_found", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "raw_fetch_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(text))
}

// handleMailMessageAttachment — GET /api/mail/messages/{mid}/attachments/{idx}
// By default returns AttachmentInfo JSON. With ?download=1, streams the cached
// attachment body from the read-only Mail index cache.
func (s *Server) handleMailMessageAttachment(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	messageID := r.PathValue("mid")
	idx := parseInt(r.PathValue("idx"))
	if idx < 0 {
		writeError(w, http.StatusBadRequest, "invalid_index", "attachment index must be >= 0")
		return
	}
	if r.URL.Query().Get("download") == "1" {
		file, err := s.mail.MailAttachmentFile(r.Context(), messageID, idx)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "attachment_not_stored", err.Error())
				return
			}
			writeError(w, http.StatusBadRequest, "attachment_fetch_failed", err.Error())
			return
		}
		if file.SizeBytes > maxMailAttachmentDownloadBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "attachment_too_large", "attachment exceeds maximum download size")
			return
		}
		var rs io.ReadSeeker
		var closer io.Closer
		if file.Reader != nil {
			defer file.Reader.Close()
		} else {
			f, err := os.Open(file.Path)
			if err != nil {
				writeError(w, http.StatusNotFound, "attachment_not_stored", "attachment cache file not found")
				return
			}
			defer f.Close()
			rs = f
			closer = f
		}
		_ = closer
		if file.ContentType != "" {
			w.Header().Set("Content-Type", file.ContentType)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		filename := file.Filename
		if filename == "" {
			filename = fmt.Sprintf("attachment-%d", idx)
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if file.Reader != nil {
			_, _ = io.Copy(w, file.Reader)
			return
		}
		http.ServeContent(w, r, filename, time.Now(), rs)
		return
	}
	info, err := s.mail.MailAttachment(r.Context(), messageID, idx)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "attachment_not_stored", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "attachment_fetch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleMailMessageSearch — POST /api/mail/accounts/{id}/search
// FTS5 query.  Default limit=50, max=200, offset=0.
func (s *Server) handleMailMessageSearch(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	accountID := r.PathValue("id")
	var body struct {
		Query         string   `json:"query"`
		AccountIDs    []string `json:"account_ids"`
		Scope         string   `json:"scope"`
		FromDomain    string   `json:"from_domain"`
		To            string   `json:"to"`
		Since         string   `json:"since"`
		Before        string   `json:"before"`
		HasAttachment *bool    `json:"has_attachment"`
		UnreadOnly    bool     `json:"unread_only"`
		Limit         int      `json:"limit"`
		Offset        int      `json:"offset"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Limit <= 0 {
		body.Limit = 50
	}
	if body.Limit > 200 {
		body.Limit = 200
	}
	if body.Offset < 0 {
		body.Offset = 0
	}
	// If caller supplied a list of account IDs, use it; otherwise constrain
	// to the URL-scoped single ID.
	ids := body.AccountIDs
	if len(ids) == 0 {
		ids = []string{accountID}
	}
	resp, err := s.mail.MailMessageSearch(r.Context(), mail.MailSearchQuery{
		AccountIDs:    ids,
		Query:         body.Query,
		Scope:         body.Scope,
		FromDomain:    body.FromDomain,
		To:            body.To,
		Since:         body.Since,
		Before:        body.Before,
		HasAttachment: body.HasAttachment,
		UnreadOnly:    body.UnreadOnly,
		Limit:         body.Limit,
		Offset:        body.Offset,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailIndexHealthGet — GET /api/mail/accounts/{id}/index/health
func (s *Server) handleMailIndexHealthGet(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	accountID := r.PathValue("id")
	h, err := s.mail.MailIndexHealthGet(r.Context(), accountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "index_health_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// handleMailIndexHealthList — GET /api/mail/index/health
func (s *Server) handleMailIndexHealthList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	out, err := s.mail.MailIndexHealthList(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "index_health_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// handleMailIndexHealthReset — POST /api/mail/accounts/{id}/index/reset
// HIGH destructive: drops indexed rows and schedules a full rebuild.
func (s *Server) handleMailIndexHealthReset(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	accountID := r.PathValue("id")
	h, err := s.mail.MailIndexHealthReset(r.Context(), accountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "index_reset_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// handleMailImapSyncStart — POST /api/mail/accounts/{id}/sync/start
func (s *Server) handleMailImapSyncStart(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	accountID := r.PathValue("id")
	if err := s.mail.MailImapSyncStart(r.Context(), accountID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account_not_found", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "sync_control_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": "syncing"})
}

// handleMailImapSyncPause — POST /api/mail/accounts/{id}/sync/pause
func (s *Server) handleMailImapSyncPause(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	accountID := r.PathValue("id")
	if err := s.mail.MailImapSyncPause(r.Context(), accountID); err != nil {
		writeError(w, http.StatusBadRequest, "sync_control_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": "paused"})
}

// handleMailImapSyncResume — POST /api/mail/accounts/{id}/sync/resume
func (s *Server) handleMailImapSyncResume(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	accountID := r.PathValue("id")
	if err := s.mail.MailImapSyncResume(r.Context(), accountID); err != nil {
		writeError(w, http.StatusBadRequest, "sync_control_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": "syncing"})
}

// handleMailImapSyncReset — POST /api/mail/accounts/{id}/sync/reset
// HIGH destructive: clears UID watermarks and forces a full re-sync.
func (s *Server) handleMailImapSyncReset(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	accountID := r.PathValue("id")
	if err := s.mail.MailImapSyncReset(r.Context(), accountID); err != nil {
		writeError(w, http.StatusBadRequest, "imap_sync_state_invalid", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": "reset"})
}

// handleMailComposeSend — POST /api/mail/compose/send
// Validates that `from` is in the registered account address list (returns
// 400 / from_address_not_registered if not).  To/CC/BCC must be valid RFC5322.
func (s *Server) handleMailComposeSend(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var req mail.ComposeSendRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.mail.MailComposeSend(r.Context(), req)
	if err != nil {
		code := "compose_failed"
		status := http.StatusBadRequest
		msg := err.Error()
		if msg == "from_address_not_registered" {
			code = "from_address_not_registered"
			status = http.StatusBadRequest
		} else if errors.Is(err, storage.ErrNotFound) {
			code = "account_not_found"
			status = http.StatusNotFound
		}
		writeError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusAccepted, resp)
}

// handleMailDraftSave — POST /api/mail/drafts
func (s *Server) handleMailDraftSave(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var req mail.DraftSaveRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.mail.MailDraftSave(r.Context(), req)
	if err != nil {
		if writeMailCapabilityUnavailable(w, err) {
			return
		}
		writeError(w, http.StatusBadRequest, "draft_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMailDraftDelete — DELETE /api/mail/drafts/{did}
func (s *Server) handleMailDraftDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	draftID := r.PathValue("did")
	if err := s.mail.MailDraftDelete(r.Context(), draftID); err != nil {
		if writeMailCapabilityUnavailable(w, err) {
			return
		}
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "draft_not_found", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "draft_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- Group 8: Logs + Backup + Retention + Danger Zone

// ---------------------------------------------------------------------------
// Logs handlers (read-only, no CSRF)
// ---------------------------------------------------------------------------

// handleMailLogsList — GET /api/mail/logs
// Lists every visible log file under the mox logs directory.  Read-only.
func (s *Server) handleMailLogsList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	list, err := s.mail.MailLogsList(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "logs_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// handleMailLogsTail — GET /api/mail/logs/tail
// Returns the last N (redacted) lines of a log file, optionally filtered
// by search/severity.  `limit` is clamped to [1, 1000].
func (s *Server) handleMailLogsTail(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	q := r.URL.Query()
	path := q.Get("path")
	limit := parseInt(q.Get("limit"))
	if limit < 1 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	search := q.Get("search")
	severity := q.Get("severity")
	res, err := s.mail.MailLogsTail(r.Context(), path, limit, search, severity)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "path_not_allowed" {
			writeError(w, http.StatusBadRequest, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "logs_tail_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleMailLogsStream — GET /api/mail/logs/stream
// Opens a long-lived SSE stream that emits (redacted) log lines as they
// are written.  Sample rate is one of: high | normal | low.
func (s *Server) handleMailLogsStream(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	q := r.URL.Query()
	path := q.Get("path")
	sampleRate := q.Get("sample_rate")
	if sampleRate == "" {
		sampleRate = "normal"
	}
	switch sampleRate {
	case "high", "normal", "low":
	default:
		writeError(w, http.StatusBadRequest, "sample_rate_invalid",
			"sample_rate must be one of: high, normal, low")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_not_supported",
			"ResponseWriter does not support streaming")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Done signal – the service stream returns on ctx.Done(); the heartbeat
	// goroutine uses the same context.
	type sseFrame struct {
		event string
		data  any
	}
	// Buffered channel so the service callbacks don't block when the
	// client's TCP send window is momentarily full.
	ch := make(chan sseFrame, 128)
	done := make(chan struct{})

	// Start the service stream in a goroutine.
	go func() {
		defer close(done)
		_ = s.mail.MailLogsStream(ctx, path, sampleRate, mail.MailLogsStreamEvent{
			OnLine: func(line string) bool {
				select {
				case <-ctx.Done():
					return false
				case ch <- sseFrame{event: "line", data: line}:
					return true
				}
			},
			OnSkipped: func(n int) bool {
				select {
				case <-ctx.Done():
					return false
				case ch <- sseFrame{event: "skipped", data: map[string]int{"count": n}}:
					return true
				}
			},
			OnHeartbeat: func() bool {
				select {
				case <-ctx.Done():
					return false
				case ch <- sseFrame{event: "heartbeat", data: map[string]string{"at": time.Now().UTC().Format(time.RFC3339)}}:
					return true
				}
			},
		})
	}()

	// Explicit heartbeat ticker as a belt-and-suspenders safety net: if the
	// service stream stalls for any reason the client still gets a periodic
	// ping so reverse proxies (nginx, cloudflare) don't drop the connection.
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				select {
				case ch <- sseFrame{event: "heartbeat", data: map[string]string{"at": time.Now().UTC().Format(time.RFC3339)}}:
				case <-ctx.Done():
				}
			}
		}
	}()

	// Drain the channel and write SSE frames.  Detect client disconnect via
	// the request context (http.Request.Context is cancelled on close).
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			// Service stream exited – drain any queued frames then return.
		drain:
			for {
				select {
				case frame := <-ch:
					writeSSEFrame(w, flusher, frame.event, frame.data)
				default:
					break drain
				}
			}
			return
		case frame, ok := <-ch:
			if !ok {
				return
			}
			writeSSEFrame(w, flusher, frame.event, frame.data)
		}
	}
}

// writeSSEFrame is a tiny helper that marshals `data` as JSON and writes a
// single event: … / data: … / pair to w, then flushes.  Kept local to the
// Group 8 handlers because it is tailored to the logs SSE shape.
func writeSSEFrame(w http.ResponseWriter, flusher http.Flusher, event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(payload))
	flusher.Flush()
}

// handleMailLogsRedactionSummary — GET /api/mail/logs/redaction-summary
// Informational endpoint.  Returns the count and human-readable descriptions
// of every redaction rule currently active.  No secrets are exposed.
func (s *Server) handleMailLogsRedactionSummary(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	sum, err := s.mail.MailLogsRedactionSummary(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "redaction_summary_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// ---------------------------------------------------------------------------
// Backup handlers (reads: no CSRF; writes: CSRF + drift + RO)
// ---------------------------------------------------------------------------

// handleMailBackupList — GET /api/mail/backups
func (s *Server) handleMailBackupList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	q := r.URL.Query()
	scope := q.Get("scope")
	limit := parseInt(q.Get("limit"))
	offset := parseInt(q.Get("offset"))
	if limit <= 0 {
		limit = 50
	}
	list, total, err := s.mail.MailBackupList(r.Context(), scope, limit, offset)
	if err != nil {
		writeError(w, http.StatusBadRequest, "backup_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  list,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// handleMailBackupCreate — POST /api/mail/backups
// HIGH-audit operator action.  Responds 201 for small backups (<=100MB) and
// 202 Accepted with a `location` header for larger ones so the UI can poll.
func (s *Server) handleMailBackupCreate(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var body struct {
		Scope string `json:"scope"`
		Note  string `json:"note"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	rec, err := s.mail.MailBackupCreate(r.Context(), body.Scope, body.Note)
	if err != nil {
		writeError(w, http.StatusBadRequest, "backup_create_failed", err.Error())
		return
	}
	const oneHundredMB = 100 * 1024 * 1024
	if rec.SizeBytes > oneHundredMB {
		w.Header().Set("Location", "/api/mail/backups/"+rec.ID)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"job_id":  rec.ID,
			"state":   rec.State,
			"created": rec.CreatedAtISO,
			"id":      rec.ID,
		})
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// handleMailBackupDownload — GET /api/mail/backups/{bid}
// Serves the raw backup artefact with Content-Disposition: attachment.
func (s *Server) handleMailBackupDownload(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	bid := r.PathValue("bid")
	rec, err := s.mail.MailBackupGet(r.Context(), bid)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "backup_not_found" {
			writeError(w, http.StatusNotFound, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "backup_download_failed", err.Error())
		return
	}
	if rec.FilePath == "" || rec.FileName == "" {
		writeError(w, http.StatusNotFound, "backup_not_found", "backup artefact missing from disk")
		return
	}
	// Pre-empt the content-disposition / type that http.ServeFile would
	// otherwise guess.  We deliberately opt into "attachment" so browsers
	// download rather than display the tarball.
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=\"%s\"", rec.FileName))
	http.ServeFile(w, r, rec.FilePath)
}

// handleMailBackupDelete — DELETE /api/mail/backups/{bid}
// HIGH-danger operator action.
func (s *Server) handleMailBackupDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	bid := r.PathValue("bid")
	if err := s.mail.MailBackupDelete(r.Context(), bid); err != nil {
		code := mail.ErrorCode(err)
		if code == "backup_not_found" {
			writeError(w, http.StatusNotFound, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "backup_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": bid})
}

// handleMailBackupScheduleList — GET /api/mail/backup/schedules
func (s *Server) handleMailBackupScheduleList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	list, err := s.mail.MailBackupScheduleList(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "backup_schedule_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// handleMailBackupScheduleUpsert — POST /api/mail/backup/schedules
func (s *Server) handleMailBackupScheduleUpsert(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var body mail.MailBackupSchedule
	if !decodeJSON(w, r, &body) {
		return
	}
	sch, err := s.mail.MailBackupScheduleUpsert(r.Context(), body)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "retention_rule_invalid" {
			writeError(w, http.StatusBadRequest, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "backup_schedule_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sch)
}

// handleMailBackupScheduleDelete — DELETE /api/mail/backup/schedules/{sid}
func (s *Server) handleMailBackupScheduleDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	sid := r.PathValue("sid")
	if err := s.mail.MailBackupScheduleDelete(r.Context(), sid); err != nil {
		writeError(w, http.StatusBadRequest, "backup_schedule_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": sid})
}

// ---------------------------------------------------------------------------
// Retention handlers
// ---------------------------------------------------------------------------

// handleMailRetentionList — GET /api/mail/retention/rules
func (s *Server) handleMailRetentionList(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	list, err := s.mail.MailRetentionList(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "retention_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// handleMailRetentionUpsert — POST /api/mail/retention/rules
func (s *Server) handleMailRetentionUpsert(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var body mail.MailRetentionRule
	if !decodeJSON(w, r, &body) {
		return
	}
	rule, err := s.mail.MailRetentionUpsert(r.Context(), body)
	if err != nil {
		code := mail.ErrorCode(err)
		if code == "retention_rule_invalid" {
			writeError(w, http.StatusBadRequest, code, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "retention_rule_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// handleMailRetentionDelete — DELETE /api/mail/retention/rules/{rid}
func (s *Server) handleMailRetentionDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	rid := r.PathValue("rid")
	if err := s.mail.MailRetentionDelete(r.Context(), rid); err != nil {
		writeError(w, http.StatusBadRequest, "retention_rule_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": rid})
}

// handleMailRetentionApplyNow — POST /api/mail/retention/apply-now
// Applies every rule immediately and returns a scope→deleted_count summary.
func (s *Server) handleMailRetentionApplyNow(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	summary, err := s.mail.MailRetentionApplyNow(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "retention_apply_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// ---------------------------------------------------------------------------
// Danger Zone handlers
// ---------------------------------------------------------------------------

// handleMailDangerGenerateCode — POST /api/mail/danger/generate-code
// Generates a one-time 6-digit code and its validity window.  No CSRF
// payload body (no body) but the request still requires auth + CSRF so
// cross-origin pages can't silently trigger code emission.
func (s *Server) handleMailDangerGenerateCode(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	code, err := s.mail.MailDangerDeleteGenerateCode(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "danger_code_generate_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, code)
}

// handleMailDangerHardDelete — POST /api/mail/danger/hard-delete
// HIGH-danger action.  Validates 3 checkboxes, 60s countdown, 6-digit code,
// and account-name confirmation before wiping every mox-owned file.
func (s *Server) handleMailDangerHardDelete(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, sess.Session) {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	if s.checkMailImportRO(w, r.Context()) {
		return
	}
	if s.checkMailDrift(w, r.Context()) {
		return
	}
	var body mail.DangerDeleteConfirmation
	if !decodeJSON(w, r, &body) {
		return
	}
	// HTTP-level shape check before delegating to the service: all fields
	// must be present (all 3 checkboxes true, countdown int sane, code not
	// empty, account_name present).  The service re-validates these so it
	// remains independently correct; doing the check here keeps our HTTP
	// error codes explicit.
	allChecked := body.ThreeCheckboxes[0] && body.ThreeCheckboxes[1] && body.ThreeCheckboxes[2]
	if !allChecked {
		writeError(w, http.StatusBadRequest, "danger_checkboxes_incomplete",
			"exactly 3 checkboxes required")
		return
	}
	if body.RandomVerificationCode == "" {
		writeError(w, http.StatusBadRequest, "danger_code_mismatch",
			"verification_code is required")
		return
	}
	if body.SixtySecondCountdownElapsedSec < 0 {
		writeError(w, http.StatusBadRequest, "danger_countdown_incomplete",
			"countdown_elapsed_seconds must be non-negative")
		return
	}
	if body.AccountName == "" {
		writeError(w, http.StatusBadRequest, "danger_account_mismatch",
			"account_name is required")
		return
	}
	result, err := s.mail.MailDangerHardDelete(r.Context(), body)
	if err != nil {
		code := mail.ErrorCode(err)
		switch code {
		case "danger_code_expired":
			writeError(w, http.StatusGone, code, err.Error())
		case "danger_code_mismatch":
			writeError(w, http.StatusBadRequest, code, err.Error())
		case "danger_countdown_incomplete":
			writeError(w, http.StatusBadRequest, code, err.Error())
		case "danger_checkboxes_incomplete":
			writeError(w, http.StatusBadRequest, code, err.Error())
		case "danger_account_mismatch":
			writeError(w, http.StatusBadRequest, code, err.Error())
		default:
			writeError(w, http.StatusBadRequest, "danger_delete_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleMailDangerRequirements — GET /api/mail/danger/requirements
// Returns the static shape of the danger-zone UI so the frontend does not
// need to hard-code the 60s countdown / 3 checkboxes / 6-digit rules.
// Read-only, no CSRF.
func (s *Server) handleMailDangerRequirements(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "mail_not_wired", "Mail 服务未接入")
		return
	}
	writeJSON(w, http.StatusOK, s.mail.DangerRequirements(r.Context()))
}
