package codexclient

import (
	"os"
	"strings"
	"testing"

	"phantom-lancer/internal/storage"
)

func TestBuildChildEnvDropsSecrets(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("HOME", "/home/owner")
	t.Setenv("PL_SESSION_SECRET", "super-secret")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("DATABASE_TOKEN", "tok")

	env := BuildChildEnv("")
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "PATH=/usr/bin") {
		t.Fatalf("expected PATH to be forwarded, got %v", env)
	}
	for _, leaked := range []string{"PL_SESSION_SECRET", "OPENAI_API_KEY", "DATABASE_TOKEN", "super-secret", "sk-test"} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("secret env leaked into child env: %s in %v", leaked, env)
		}
	}
}

func TestBuildChildEnvForwardsCodexHome(t *testing.T) {
	os.Unsetenv("CODEX_HOME")
	env := BuildChildEnv("/data/codex-home")
	if !strings.Contains(strings.Join(env, "\n"), "CODEX_HOME=/data/codex-home") {
		t.Fatalf("expected explicit CODEX_HOME, got %v", env)
	}
}

func TestResolveRunPolicyTrustEnforced(t *testing.T) {
	policy := NewWorkspacePolicy(func() ([]string, error) { return []string{"/srv"}, nil })

	untrusted := storage.CodexCliWorkspace{TrustState: "untrusted", DefaultSandbox: "read-only", DefaultApprovalPolicy: "on-request"}
	if _, err := policy.ResolveRunPolicy(untrusted, "workspace-write", "on-request"); err == nil {
		t.Fatal("expected untrusted workspace to reject workspace-write")
	}

	trusted := storage.CodexCliWorkspace{TrustState: "trusted", DefaultSandbox: "workspace-write", DefaultApprovalPolicy: "on-request", NetworkPolicy: map[string]any{"enabled": false}}
	resolved, err := policy.ResolveRunPolicy(trusted, "workspace-write", "on-request")
	if err != nil {
		t.Fatalf("expected trusted workspace-write to be allowed: %v", err)
	}
	if resolved.Sandbox != "workspace-write" || resolved.NetworkEnabled {
		t.Fatalf("unexpected resolved policy: %+v", resolved)
	}
}

func TestExecBuildArgsNeverYolo(t *testing.T) {
	client := NewExecClient()
	args := client.BuildArgs(ExecOptions{Sandbox: "read-only", Approval: "on-request", Model: "gpt-x", Prompt: "hi", Images: []string{"/tmp/a.png"}})
	joined := strings.Join(args, " ")
	for _, banned := range []string{"--yolo", "--dangerously-bypass-approvals-and-sandbox", "full-access"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("exec args contain banned flag %q: %v", banned, args)
		}
	}
	if !strings.Contains(joined, "--sandbox read-only") || !strings.Contains(joined, "--ask-for-approval on-request") {
		t.Fatalf("expected sandbox/approval flags, got %v", args)
	}
	if !strings.Contains(joined, "--image /tmp/a.png") {
		t.Fatalf("expected image flag, got %v", args)
	}
}

func TestEventMapperMapsApprovalRequest(t *testing.T) {
	mapper := NewEventMapper(200)
	req := ServerRequest{ID: []byte(`7`), Method: MethodCommandApproval, Params: []byte(`{"itemId":"item-1","command":"rm -rf /tmp/x","cwd":"/srv/app"}`)}
	approval, ok := mapper.ParseApprovalRequest(req)
	if !ok {
		t.Fatal("expected approval request to parse")
	}
	if approval.CodexRequestID != "item-1" {
		t.Fatalf("expected itemId as request id, got %q", approval.CodexRequestID)
	}
	if approval.RiskLevel != "high" {
		t.Fatalf("expected high risk for rm command, got %s", approval.RiskLevel)
	}
}

func TestEventMapperIgnoresNonApprovalRequest(t *testing.T) {
	mapper := NewEventMapper(200)
	if _, ok := mapper.ParseApprovalRequest(ServerRequest{ID: []byte(`1`), Method: "thread/started"}); ok {
		t.Fatal("expected non-approval request to be ignored")
	}
}

func TestEventMapperAppServerTurnCompleted(t *testing.T) {
	mapper := NewEventMapper(200)
	event, ok := mapper.MapAppServerNotification("turn/completed", []byte(`{"turn":{"status":"completed"}}`))
	if !ok || event.TurnStatus != "completed" {
		t.Fatalf("expected completed turn status, got ok=%v event=%+v", ok, event)
	}
	event, ok = mapper.MapAppServerNotification("turn/completed", []byte(`{"turn":{"status":"interrupted"}}`))
	if !ok || event.TurnStatus != "cancelled" {
		t.Fatalf("expected cancelled turn status for interrupted, got %+v", event)
	}
}

func TestEventMapperAppServerAgentMessage(t *testing.T) {
	mapper := NewEventMapper(200)
	event, ok := mapper.MapAppServerNotification("item/completed", []byte(`{"item":{"type":"agentMessage","text":"hello"}}`))
	if !ok || event.EventType != EventMessageAgent || event.TextPreview != "hello" {
		t.Fatalf("unexpected agent message mapping: ok=%v %+v", ok, event)
	}
}

func TestEventMapperExecItemSnakeCase(t *testing.T) {
	mapper := NewEventMapper(200)
	event, ok := mapper.MapExecLine([]byte(`{"type":"item.completed","item":{"type":"command_execution","command":"ls"}}`))
	if !ok || event.EventType != EventCommandDone {
		t.Fatalf("unexpected exec command mapping: ok=%v %+v", ok, event)
	}
}

func TestExecTurnInputBuildsArray(t *testing.T) {
	input := buildTurnInput("hi", []string{"/tmp/a.png"})
	if len(input) != 2 {
		t.Fatalf("expected text + image input, got %d", len(input))
	}
	if input[0]["type"] != "text" || input[0]["text"] != "hi" {
		t.Fatalf("unexpected text input: %+v", input[0])
	}
	if input[1]["type"] != "localImage" || input[1]["path"] != "/tmp/a.png" {
		t.Fatalf("unexpected image input: %+v", input[1])
	}
}

func TestAppServerPolicyMapping(t *testing.T) {
	// AskForApproval values are kebab-case per the v2 schema.
	if got := appServerApprovalPolicy("on-request"); got != "on-request" {
		t.Fatalf("expected on-request, got %s", got)
	}
	if got := appServerApprovalPolicy("unless-trusted"); got != "untrusted" {
		t.Fatalf("expected untrusted, got %s", got)
	}
	// thread/start uses the SandboxMode string.
	if got := appServerSandboxMode("workspace-write"); got != "workspace-write" {
		t.Fatalf("expected workspace-write sandbox mode, got %s", got)
	}
	if got := appServerSandboxMode(""); got != "read-only" {
		t.Fatalf("expected read-only default sandbox mode, got %s", got)
	}
	// turn/start uses the sandboxPolicy object.
	ro := appServerSandboxPolicy(RunPolicy{Sandbox: "read-only"}, "/srv/app")
	if ro["type"] != "readOnly" {
		t.Fatalf("expected readOnly sandbox, got %+v", ro)
	}
	ww := appServerSandboxPolicy(RunPolicy{Sandbox: "workspace-write", NetworkEnabled: false}, "/srv/app")
	if ww["type"] != "workspaceWrite" {
		t.Fatalf("expected workspaceWrite sandbox, got %+v", ww)
	}
	roots, ok := ww["writableRoots"].([]string)
	if !ok || len(roots) != 1 || roots[0] != "/srv/app" {
		t.Fatalf("expected writableRoots with workspace path, got %+v", ww["writableRoots"])
	}
}

func TestRedactStripsTokens(t *testing.T) {
	out := Redact("authorization: Bearer abc123 access_token=secretvalue", 0)
	if strings.Contains(out, "abc123") || strings.Contains(out, "secretvalue") {
		t.Fatalf("redact failed: %s", out)
	}
}

func TestNormalizeSettingsBounds(t *testing.T) {
	s := normalizeSettings(Settings{AppServerProbeSeconds: 1, MaxEventsPerThread: 0, DefaultSandbox: "bogus"})
	if s.AppServerProbeSeconds < 5 {
		t.Fatalf("probe interval not clamped: %d", s.AppServerProbeSeconds)
	}
	if s.MaxEventsPerThread <= 0 {
		t.Fatalf("max events not defaulted: %d", s.MaxEventsPerThread)
	}
	if s.DefaultSandbox != "read-only" {
		t.Fatalf("invalid sandbox not reset: %s", s.DefaultSandbox)
	}
}
