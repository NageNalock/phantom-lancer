package configapply

// StepStatus represents one step's status streamed through a progress channel
// (used for SSE / UI step-by-step progress).
type StepStatus struct {
	Step    int    `json:"step"`    // 1..10
	Total   int    `json:"total"`     // always 10
	Name    string `json:"name"`
	Percent int    `json:"percent"` // 0..100 cumulative
	Message string `json:"message,omitempty"`
	Output  string `json:"output,omitempty"`
	// State: "running" | "done" | "failed" | "rollback"
	State string `json:"state"`
}

// PipelineResult is the terminal outcome of a Run() call.
type PipelineResult struct {
	Success     bool         `json:"success"`
	Steps       []StepStatus `json:"steps"`
	FailureStep int          `json:"failure_step"`
	RolledBack  bool         `json:"rolled_back"`
	RollbackErr string       `json:"rollback_err,omitempty"`
	ConfigHash  string       `json:"config_hash,omitempty"`
	Summary     string       `json:"summary,omitempty"`
}

// LocalConfigTestResult / LocalParsedConfig are small local copies of the moxcli
// return types, so configapply has NO import coupling to moxcli.
// RunnerInterface is declared against these.
type LocalConfigTestResult struct {
	OK       bool
	Output   string
	Errors   []string
	Warnings []string
}
type LocalParsedConfig map[string]any

// RunnerInterface is the narrow surface the pipeline needs from the CLI runner.
// The ctx parameter uses `interface{}` so the method signatures are the same
// whether the caller passes context.Context or a test mock.
type RunnerInterface interface {
	ConfigTest(ctx interface{}) (*LocalConfigTestResult, error)
	ConfigList(ctx interface{}) (LocalParsedConfig, error)
}

// 10 Step names — stable, used for rendering UI progress bar order.
const StepCount = 10

// StepNames: index 0 = step 1 (1-based UI index).
var StepNames = [StepCount]string{
	"ValidatePhantomSettings", // 0 → step 1
	"BuildConfigSkeleton",
	"ConfigTestCLI",
	"CreateTmpPath",
	"BackupActive",
	"AtomicSwap",
	"ReloadOrRestart",
	"PostApplyConfigList",
	"ProbeLayersL1_L2_L3",
	"PersistSyncedFlag",
}

// Snapshots — caller builds these from SQLite rows (no import coupling).
type SettingsSnapshot struct {
	Hostname, AdminEmail, WebmailAddr, WebAPIAddr string
	MoxBinaryPath, MoxConfigPath, MoxDataDir      string
	SMTPPort, SMTPSubmissionPort, SMTPSPort       int
	IMAPPort, IMAPSPort                           int
	ConfigHost                                    string
	MoxVersion                                    string
}
type DomainSnapshot struct {
	Domain, DKIMSelector, DMARCPolicy, DMARCRUA, SPFInclude string
}
type AccountSnapshot struct {
	Email, DisplayName, Role string
	Enabled                  bool
}
type AliasSnapshot struct {
	AliasAddr  string
	Mode       string
	Recipients []string
	Enabled    bool
}
