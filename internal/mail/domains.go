package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"phantom-lancer/internal/mail/configapply"
	"phantom-lancer/internal/mail/moxcli"
	"phantom-lancer/internal/mail/probes"
	"phantom-lancer/internal/storage"
)

// --- Domain service layer (Phase 3) -------------------------------------
//
// These methods mutate SQLite (delegating to storage) and then kick off a
// synchronous configapply pipeline run so the on-disk mox.conf reflects the
// change.

// DomainCreate inserts a new MailDomain row and runs the 10-step pipeline.
func (s *Service) DomainCreate(ctx context.Context, d storage.MailDomain) (*storage.MailDomain, error) {
	created, err := s.store.MailCreateDomain(ctx, d)
	if err != nil {
		return nil, fmt.Errorf("store create domain: %w", err)
	}
	s.touchLastChange()
	pr := s.applyFromDomains(ctx)
	if pr != nil && !pr.Success {
		return created, fmt.Errorf("pipeline failed at step %d: %s", pr.FailureStep, pr.Summary)
	}
	return created, nil
}

// DomainUpdate updates a MailDomain row and re-runs the pipeline.
func (s *Service) DomainUpdate(ctx context.Context, d storage.MailDomain) (*storage.MailDomain, error) {
	if d.ID == "" {
		return nil, errors.New("domain id is required for update")
	}
	updated, err := s.store.MailUpdateDomain(ctx, d)
	if err != nil {
		return nil, fmt.Errorf("store update domain: %w", err)
	}
	s.touchLastChange()
	pr := s.applyFromDomains(ctx)
	if pr != nil && !pr.Success {
		return updated, fmt.Errorf("pipeline failed at step %d: %s", pr.FailureStep, pr.Summary)
	}
	return updated, nil
}

// DomainDelete removes a domain row and re-runs the pipeline.
func (s *Service) DomainDelete(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("domain id is required for delete")
	}
	if err := s.store.MailDeleteDomain(ctx, id); err != nil {
		return fmt.Errorf("store delete domain: %w", err)
	}
	s.touchLastChange()
	pr := s.applyFromDomains(ctx)
	if pr != nil && !pr.Success {
		return fmt.Errorf("pipeline failed at step %d: %s", pr.FailureStep, pr.Summary)
	}
	return nil
}

// DomainEnable flips the Enabled flag on a domain.
func (s *Service) DomainEnable(ctx context.Context, id string, enable bool) (*storage.MailDomain, error) {
	cur, err := s.store.MailGetDomain(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get domain %s: %w", id, err)
	}
	if cur == nil {
		return nil, storage.ErrNotFound
	}
	cur.Enabled = enable
	return s.DomainUpdate(ctx, *cur)
}

// DomainList returns all stored domains.
func (s *Service) DomainList(ctx context.Context) ([]*storage.MailDomain, error) {
	return s.store.MailListDomains(ctx)
}

// DomainDNSCheck runs an L6-equivalent check and stores the JSON outcome.
func (s *Service) DomainDNSCheck(ctx context.Context, id string) (*storage.MailDomain, error) {
	cur, err := s.store.MailGetDomain(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get domain %s: %w", id, err)
	}
	if cur == nil {
		return nil, storage.ErrNotFound
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	out := map[string]any{
		"checked_at": ts,
		"domain":     cur.Domain,
		"status":     "skeleton",
		"records": map[string]string{
			"MX":     "not checked",
			"SPF":    "not checked",
			"DKIM":   "not checked",
			"DMARC":  "not checked",
			"TLSA":   "not checked",
			"TLSRPT": "not checked",
			"PTR":    "not checked",
			"SRV":    "not checked",
		},
	}
	buf, _ := json.Marshal(out)
	cur.DNSCheckJSON = string(buf)
	cur.LastDNSCheckAt = ts
	updated, uerr := s.store.MailUpdateDomain(ctx, *cur)
	if uerr != nil {
		return nil, fmt.Errorf("persist dns check: %w", uerr)
	}
	return updated, nil
}

// DomainDNSRecords returns the 8 recommended DNS record templates for a domain.
func (s *Service) DomainDNSRecords(ctx context.Context, id string) (map[string]string, error) {
	cur, err := s.store.MailGetDomain(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get domain %s: %w", id, err)
	}
	if cur == nil {
		return nil, storage.ErrNotFound
	}
	d := cur.Domain
	sel := cur.DKIMSelector
	if sel == "" {
		sel = "default"
	}
	recs := map[string]string{
		"MX":     fmt.Sprintf("%s.  300 IN MX  10 mail.%s.", d, d),
		"SPF":    fmt.Sprintf("%s.  300 IN TXT \"v=spf1 mx -all\"", d),
		"DKIM":   fmt.Sprintf("%s._domainkey.%s.  300 IN TXT \"v=DKIM1; k=rsa; p=...\"", sel, d),
		"DMARC":  fmt.Sprintf("_dmarc.%s.  300 IN TXT \"v=DMARC1; p=%s; rua=mailto:%s\"", d, dmarcDefault(cur.DMARCPolicy), dmarcRuaDefault(cur.DMARCRUA, d)),
		"TLSA":   fmt.Sprintf("_25._tcp.mail.%s.  300 IN TLSA 3 1 1 <sha256-cert>", d),
		"TLSRPT": fmt.Sprintf("_smtp._tls.%s.  300 IN TXT \"v=TLSRPTv1; rua=mailto:tlsrpt@%s\"", d, d),
		"PTR":    fmt.Sprintf("<MX-IP>.in-addr.arpa.  300 IN PTR mail.%s.", d),
		"SRV":    fmt.Sprintf("_autodiscover._tcp.%s.  300 IN SRV 0 0 443 mail.%s.", d, d),
	}
	return recs, nil
}

func dmarcDefault(p string) string {
	switch p {
	case "none", "quarantine", "reject":
		return p
	}
	return "none"
}

func dmarcRuaDefault(rua, domain string) string {
	if rua != "" {
		return rua
	}
	return "postmaster@" + domain
}

// applyFromDomains reads settings + domains + accounts from storage, builds
// snapshots, and runs the 10-step configapply pipeline synchronously.  A nil
// progress channel is passed (no streaming progress for this caller).
func (s *Service) applyFromDomains(ctx context.Context) *configapply.PipelineResult {
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil || settings == nil {
		return &configapply.PipelineResult{
			Success:     false,
			FailureStep: 1,
			Summary:     fmt.Sprintf("cannot load settings: %v", err),
		}
	}
	domains, derr := s.store.MailListDomains(ctx)
	accounts, _ := s.store.MailListAccounts(ctx, "", "")

	ss := configapply.SettingsSnapshot{
		Hostname:           settings.Hostname,
		AdminEmail:         settings.AdminEmail,
		WebmailAddr:        settings.WebmailAddr,
		WebAPIAddr:         settings.WebAPIAddr,
		MoxBinaryPath:      settings.MoxBinaryPath,
		MoxConfigPath:      settings.MoxConfigPath,
		MoxDataDir:         settings.MoxDataDir,
		SMTPPort:           settings.SMTPPort,
		SMTPSubmissionPort: settings.SMTPSubmissionPort,
		SMTPSPort:          settings.SMTPSPort,
		IMAPPort:           settings.IMAPPort,
		IMAPSPort:          settings.IMAPSPort,
	}
	// Fallback to s.cli paths if settings row is empty (managed mode).
	if ss.MoxBinaryPath == "" && s.cli != nil {
		ss.MoxBinaryPath = s.cli.BinaryPath
		ss.MoxConfigPath = s.cli.ConfigPath
		ss.MoxDataDir = s.cli.DataDir
	}

	ds := make([]configapply.DomainSnapshot, 0, len(domains))
	if derr == nil {
		for _, d := range domains {
			if d == nil || !d.Enabled {
				continue
			}
			ds = append(ds, configapply.DomainSnapshot{
				Domain:       d.Domain,
				DKIMSelector: d.DKIMSelector,
				DMARCPolicy:  d.DMARCPolicy,
				DMARCRUA:     d.DMARCRUA,
				SPFInclude:   d.SPFInclude,
			})
		}
	}
	as := make([]configapply.AccountSnapshot, 0, len(accounts))
	for i := range accounts {
		a := accounts[i]
		as = append(as, configapply.AccountSnapshot{
			Email:       a.Email,
			DisplayName: a.DisplayName,
			Role:        a.Role,
			Enabled:     a.Enabled,
		})
	}
	// Alias row types not wired yet; pass empty slice.
	aliasSnap := []configapply.AliasSnapshot{}

	// Adapt *moxcli.Runner to configapply.RunnerInterface (which uses local
	// type copies to avoid import coupling between the two packages).
	var cli configapply.RunnerInterface
	if s.cli != nil {
		cli = moxCliBridge{r: s.cli}
	}

	reloadFn := func(actx context.Context) error {
		// Wired to moxsupervisor.Restart in a future phase.
		return nil
	}
	probeFn := func(actx context.Context) (string, error) {
		rs, perr := s.runAllProbes(actx, nil)
		if perr != nil {
			return "", perr
		}
		return probes.Summary(rs).String(), nil
	}

	pr := configapply.Run(ctx, ss, ds, as, aliasSnap, cli, reloadFn, probeFn, nil)
	if pr.Success && s.drift != nil && pr.ConfigHash != "" {
		s.drift.SetSynced(pr.ConfigHash)
	}
	s.addAudit(ctx, "mail.configapply", pr.Summary, map[string]any{
		"success":      pr.Success,
		"failure_step": pr.FailureStep,
		"rolled_back":  pr.RolledBack,
		"config_hash":  pr.ConfigHash,
	}, "low")
	return &pr
}

// moxCliBridge adapts *moxcli.Runner to configapply.RunnerInterface.  We
// duplicate the two tiny structs here rather than importing moxcli into
// configapply.
type moxCliBridge struct {
	r *moxcli.Runner
}

func (b moxCliBridge) ConfigTest(ctxAny interface{}) (*configapply.LocalConfigTestResult, error) {
	ctx, _ := ctxAny.(context.Context)
	if ctx == nil {
		ctx = context.Background()
	}
	res, err := b.r.ConfigTest(ctx)
	if err != nil {
		return nil, err
	}
	return &configapply.LocalConfigTestResult{
		OK:       res.OK,
		Output:   res.Output,
		Errors:   append([]string(nil), res.Errors...),
		Warnings: append([]string(nil), res.Warnings...),
	}, nil
}

func (b moxCliBridge) ConfigList(ctxAny interface{}) (configapply.LocalParsedConfig, error) {
	ctx, _ := ctxAny.(context.Context)
	if ctx == nil {
		ctx = context.Background()
	}
	res, err := b.r.ConfigList(ctx)
	if err != nil {
		return nil, err
	}
	return convertParsedConfig(res), nil
}

// convertParsedConfig converts moxcli.ParsedConfig (map[string]any) into the
// local alias type configapply.LocalParsedConfig (also map[string]any).
func convertParsedConfig(pc moxcli.ParsedConfig) configapply.LocalParsedConfig {
	out := make(configapply.LocalParsedConfig, len(pc))
	for k, v := range pc {
		if nested, ok := v.(moxcli.ParsedConfig); ok {
			out[k] = convertParsedConfig(nested)
		} else {
			out[k] = v
		}
	}
	return out
}

// -----------------------------------------------------------------------------
// ConfigApply pipeline entry points (wired to HTTP handlers).
// -----------------------------------------------------------------------------

// ConfigApplyRequest carries the payload for a full config-apply pipeline run.
type ConfigApplyRequest struct {
	Force bool `json:"force"`
}

// ConfigApplyResponse wraps the pipeline result.
type ConfigApplyResponse struct {
	Success     bool                         `json:"success"`
	FailureStep int                          `json:"failure_step"`
	RolledBack  bool                         `json:"rolled_back"`
	RollbackErr string                       `json:"rollback_err,omitempty"`
	ConfigHash  string                       `json:"config_hash"`
	Summary     string                       `json:"summary"`
	Steps       []configapply.StepStatus     `json:"steps,omitempty"`
	Drifted     bool                         `json:"drifted"`
}

// ConfigApply runs the 10-step pipeline and returns the result.  If a progress
// channel is passed, steps are streamed (closed on return).  The progress
// channel may be nil.
func (s *Service) ConfigApply(ctx context.Context, req ConfigApplyRequest, progress chan<- configapply.StepStatus) (*ConfigApplyResponse, error) {
	_ = req.Force
	pr := s.applyFromDomainsWithProgress(ctx, progress)
	if pr == nil {
		return nil, errors.New("pipeline returned nil")
	}
	return &ConfigApplyResponse{
		Success:     pr.Success,
		FailureStep: pr.FailureStep,
		RolledBack:  pr.RolledBack,
		RollbackErr: pr.RollbackErr,
		ConfigHash:  pr.ConfigHash,
		Summary:     pr.Summary,
		Steps:       pr.Steps,
		Drifted:     s.Drifted(),
	}, nil
}

// applyFromDomainsWithProgress is the same as applyFromDomains but accepts a
// progress channel for streaming.
func (s *Service) applyFromDomainsWithProgress(ctx context.Context, progress chan<- configapply.StepStatus) *configapply.PipelineResult {
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil || settings == nil {
		return &configapply.PipelineResult{
			Success:     false,
			FailureStep: 1,
			Summary:     fmt.Sprintf("cannot load settings: %v", err),
		}
	}
	domains, derr := s.store.MailListDomains(ctx)
	accounts, _ := s.store.MailListAccounts(ctx, "", "")

	ss := configapply.SettingsSnapshot{
		Hostname:           settings.Hostname,
		AdminEmail:         settings.AdminEmail,
		WebmailAddr:        settings.WebmailAddr,
		WebAPIAddr:         settings.WebAPIAddr,
		MoxBinaryPath:      settings.MoxBinaryPath,
		MoxConfigPath:      settings.MoxConfigPath,
		MoxDataDir:         settings.MoxDataDir,
		SMTPPort:           settings.SMTPPort,
		SMTPSubmissionPort: settings.SMTPSubmissionPort,
		SMTPSPort:          settings.SMTPSPort,
		IMAPPort:           settings.IMAPPort,
		IMAPSPort:          settings.IMAPSPort,
	}
	if ss.MoxBinaryPath == "" && s.cli != nil {
		ss.MoxBinaryPath = s.cli.BinaryPath
		ss.MoxConfigPath = s.cli.ConfigPath
		ss.MoxDataDir = s.cli.DataDir
	}

	ds := make([]configapply.DomainSnapshot, 0, len(domains))
	if derr == nil {
		for _, d := range domains {
			if d == nil || !d.Enabled {
				continue
			}
			ds = append(ds, configapply.DomainSnapshot{
				Domain:       d.Domain,
				DKIMSelector: d.DKIMSelector,
				DMARCPolicy:  d.DMARCPolicy,
				DMARCRUA:     d.DMARCRUA,
				SPFInclude:   d.SPFInclude,
			})
		}
	}
	as := make([]configapply.AccountSnapshot, 0, len(accounts))
	for i := range accounts {
		a := accounts[i]
		as = append(as, configapply.AccountSnapshot{
			Email:       a.Email,
			DisplayName: a.DisplayName,
			Role:        a.Role,
			Enabled:     a.Enabled,
		})
	}
	aliasSnap := []configapply.AliasSnapshot{}

	var cli configapply.RunnerInterface
	if s.cli != nil {
		cli = moxCliBridge{r: s.cli}
	}

	reloadFn := func(actx context.Context) error { return nil }
	probeFn := func(actx context.Context) (string, error) {
		rs, perr := s.runAllProbes(actx, nil)
		if perr != nil {
			return "", perr
		}
		return probes.Summary(rs).String(), nil
	}

	pr := configapply.Run(ctx, ss, ds, as, aliasSnap, cli, reloadFn, probeFn, progress)
	if pr.Success && s.drift != nil && pr.ConfigHash != "" {
		s.drift.SetSynced(pr.ConfigHash)
	}
	s.addAudit(ctx, "mail.configapply", pr.Summary, map[string]any{
		"success":      pr.Success,
		"failure_step": pr.FailureStep,
		"rolled_back":  pr.RolledBack,
		"config_hash":  pr.ConfigHash,
	}, "low")
	return &pr
}

// ConfigValidate runs the first 3 steps (validate + build skeleton + config test)
// of the pipeline without mutating disk state.
type ConfigValidateRequest struct{}
type ConfigValidateResponse struct {
	OK       bool     `json:"ok"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
	Output   string   `json:"output"`
}

func (s *Service) ConfigValidate(ctx context.Context, _ ConfigValidateRequest) (*ConfigValidateResponse, error) {
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	var errs []string
	if settings.Hostname == "" {
		errs = append(errs, "hostname required")
	}
	if settings.AdminEmail == "" {
		errs = append(errs, "admin_email required")
	}
	if s.cli == nil {
		return &ConfigValidateResponse{OK: len(errs) == 0, Errors: errs}, nil
	}
	res, terr := s.cli.ConfigTest(ctx)
	if terr != nil {
		errs = append(errs, "config test: "+terr.Error())
	}
	out := ""
	if res != nil {
		out = res.Output
		for _, e := range res.Errors {
			errs = append(errs, e)
		}
	}
	return &ConfigValidateResponse{
		OK:       len(errs) == 0 && (res == nil || res.OK),
		Errors:   errs,
		Warnings: func() []string { if res != nil { return res.Warnings } ; return nil }(),
		Output:   out,
	}, nil
}

// ConfigRollback restores the .bak version of mox.conf (if it exists) and
// resets the drift flag.
type ConfigRollbackRequest struct{}
type ConfigRollbackResponse struct {
	Restored bool   `json:"restored"`
	Message  string `json:"message"`
}

func (s *Service) ConfigRollback(ctx context.Context, _ ConfigRollbackRequest) (*ConfigRollbackResponse, error) {
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	cfgPath := settings.MoxConfigPath
	if cfgPath == "" && s.cli != nil {
		cfgPath = s.cli.ConfigPath
	}
	bakPath := cfgPath + ".bak"
	if _, serr := os.Stat(bakPath); serr != nil {
		return &ConfigRollbackResponse{Restored: false, Message: "no backup found at " + bakPath}, nil
	}
	if cerr := configapply.CopyAtomic(bakPath, cfgPath); cerr != nil {
		return nil, fmt.Errorf("restore backup: %w", cerr)
	}
	if s.drift != nil {
		newHash, _ := configapply.HashFile(cfgPath)
		s.drift.SetSynced(newHash)
	}
	s.touchLastChange()
	s.addAudit(ctx, "mail.configrollback", "restored "+bakPath+" to "+cfgPath, map[string]any{
		"backup": bakPath,
		"target": cfgPath,
	}, "high")
	return &ConfigRollbackResponse{Restored: true, Message: "backup restored"}, nil
}

// ConfigSummary returns the current drift status + disk hash for the UI summary card.
type ConfigSummaryResponse struct {
	Drifted     bool   `json:"drifted"`
	ConfigPath  string `json:"config_path"`
	ExpectedHash string `json:"expected_hash"`
	DiskHash    string `json:"disk_hash"`
	LastCheck   string `json:"last_check"`
	ConfigMode  string `json:"config_mode"`
}

func (s *Service) ConfigSummary(ctx context.Context) (*ConfigSummaryResponse, error) {
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	cfgPath := settings.MoxConfigPath
	if cfgPath == "" && s.cli != nil {
		cfgPath = s.cli.ConfigPath
	}
	sum := &ConfigSummaryResponse{
		ConfigPath: cfgPath,
		ConfigMode: settings.ConfigMode,
	}
	if s.drift != nil {
		drifted, diskHash, _ := s.drift.Refresh()
		sum.Drifted = drifted
		sum.ExpectedHash = s.drift.SQLiteHash()
		sum.DiskHash = diskHash
		sum.LastCheck = s.drift.LastCheck()
	}
	return sum, nil
}

// ResolveDriftRequest carries the resolve-drift payload.  Values match the
// TypeScript union MailResolveDriftRequest (action: "overwrite" | "reimport"):
//
//   - overwrite → re-apply from SQLite, overwriting any on-disk hand-edit
//   - reimport  → accept the on-disk version as authoritative and re-seed
//     the drift detector's expected hash from it
type ResolveDriftRequest struct {
	Action string `json:"action"` // "overwrite" or "reimport"
}
type ResolveDriftResponse struct {
	Accepted bool   `json:"accepted"`
	Action   string `json:"action"`
	NewHash  string `json:"new_hash"`
	Message  string `json:"message,omitempty"`
	Pipeline *ConfigApplyResponse `json:"pipeline,omitempty"`
}

func (s *Service) ResolveDrift(ctx context.Context, req ResolveDriftRequest) (*ResolveDriftResponse, error) {
	if s.drift == nil {
		return &ResolveDriftResponse{
			Accepted: true,
			Action:   req.Action,
			Message:  "no drift detector wired (skipping)",
		}, nil
	}
	switch req.Action {
	case "reimport":
		_, realHash, _ := s.drift.Refresh()
		s.drift.SetSynced(realHash)
		s.touchLastChange()
		s.addAudit(ctx, "mail.resolve_drift", "reimport: accepted on-disk config as authoritative",
			map[string]any{"new_hash": realHash}, "high")
		return &ResolveDriftResponse{
			Accepted: true,
			Action:   "reimport",
			NewHash:  realHash,
			Message:  "已以磁盘上的 mox.conf 为准重建基线",
		}, nil
	case "overwrite", "":
		// overwrite is the default (also triggers on empty action as a
		// gentle fallback so legacy clients don't break).
		pr, perr := s.ConfigApply(ctx, ConfigApplyRequest{Force: true}, nil)
		if perr != nil {
			return nil, perr
		}
		s.addAudit(ctx, "mail.resolve_drift", "overwrite: re-applied DB-sourced config to disk",
			map[string]any{
				"success":      pr.Success,
				"failure_step": pr.FailureStep,
				"rolled_back":  pr.RolledBack,
				"new_hash":     pr.ConfigHash,
			}, "high")
		return &ResolveDriftResponse{
			Accepted: pr.Success,
			Action:   "overwrite",
			NewHash:  pr.ConfigHash,
			Message:  pr.Summary,
			Pipeline: pr,
		}, nil
	default:
		return nil, fmt.Errorf("unknown resolve-drift action: %q (want overwrite | reimport)", req.Action)
	}
}
