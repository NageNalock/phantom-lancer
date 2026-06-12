package codexclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/storage"
)

func cleanupCodexTestService(t *testing.T, svc *Service, store *storage.Store) {
	t.Helper()
	t.Cleanup(func() {
		waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := svc.waitForAsync(waitCtx); err != nil {
			t.Errorf("wait for codex test background tasks: %v", err)
		}
		svc.Close()
		if err := store.Close(); err != nil {
			t.Errorf("close test store: %v", err)
		}
	})
}

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

	untrusted := storage.CodexCliWorkspace{TrustState: "untrusted", DefaultSandbox: "read-only", DefaultApprovalPolicy: "on-request", NetworkPolicy: map[string]any{"enabled": true}}
	if _, err := policy.ResolveRunPolicy(untrusted, "workspace-write", "on-request"); err == nil {
		t.Fatal("expected untrusted workspace to reject workspace-write")
	}
	readOnly, err := policy.ResolveRunPolicy(untrusted, "read-only", "on-request")
	if err != nil {
		t.Fatalf("expected untrusted read-only to be allowed: %v", err)
	}
	if readOnly.NetworkEnabled {
		t.Fatal("expected untrusted workspace to force network off")
	}

	trusted := storage.CodexCliWorkspace{TrustState: "trusted", DefaultSandbox: "workspace-write", DefaultApprovalPolicy: "on-request", NetworkPolicy: map[string]any{"enabled": false}}
	resolved, err := policy.ResolveRunPolicy(trusted, "workspace-write", "on-request")
	if err != nil {
		t.Fatalf("expected trusted workspace-write to be allowed: %v", err)
	}
	if resolved.Sandbox != "workspace-write" || resolved.NetworkEnabled {
		t.Fatalf("unexpected resolved policy: %+v", resolved)
	}
	if _, err := policy.ResolveRunPolicy(trusted, "workspace-write", "never"); err == nil {
		t.Fatal("expected never approval policy to be rejected")
	}
	if _, err := policy.ResolveRunPolicy(trusted, "workspace-write", "on-failure"); err == nil {
		t.Fatal("expected on-failure approval policy to be rejected")
	}
	restricted := storage.CodexCliWorkspace{TrustState: "restricted", DefaultSandbox: "read-only", DefaultApprovalPolicy: "on-request", NetworkPolicy: map[string]any{"enabled": true}}
	resolved, err = policy.ResolveRunPolicy(restricted, "read-only", "on-request")
	if err != nil {
		t.Fatalf("expected restricted read-only to be allowed: %v", err)
	}
	if resolved.NetworkEnabled {
		t.Fatal("expected restricted workspace to force network off")
	}
}

func TestCreateWorkspaceWithOptionsCreatesMissingDirectoryInsideAllowedRoot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, events.NewHub(), dir, func() ([]string, error) { return []string{dir}, nil }, nil)
	cleanupCodexTestService(t, svc, store)
	workspacePath := filepath.Join(dir, "projects", "new-app")

	if _, err := svc.CreateWorkspaceWithOptions(ctx, storage.CodexCliWorkspace{Path: workspacePath, TrustState: "trusted", DefaultSandbox: "read-only", DefaultApprovalPolicy: "on-request"}, CreateWorkspaceOptions{CreateIfMissing: false}); !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("CreateWorkspace without create option error = %v, want ErrPathNotFound", err)
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("workspace path should not be created without confirmation, stat err=%v", err)
	}

	ws, err := svc.CreateWorkspaceWithOptions(ctx, storage.CodexCliWorkspace{Path: workspacePath, TrustState: "trusted", DefaultSandbox: "read-only", DefaultApprovalPolicy: "on-request"}, CreateWorkspaceOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("CreateWorkspaceWithOptions: %v", err)
	}
	expectedPath, err := filepath.EvalSymlinks(workspacePath)
	if err != nil {
		t.Fatalf("resolve created workspace: %v", err)
	}
	if ws.Path != expectedPath {
		t.Fatalf("workspace path = %q, want %q", ws.Path, expectedPath)
	}
	info, err := os.Stat(workspacePath)
	if err != nil {
		t.Fatalf("stat created workspace: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("created workspace path is not a directory")
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

func TestDefaultModelFromCatalogPrefersLiveDefault(t *testing.T) {
	model := defaultModelFromCatalog([]CodexModel{
		{ID: "gpt-5"},
		{ID: "gpt-5-codex", IsDefault: true},
	})
	if model != "gpt-5-codex" {
		t.Fatalf("model = %q, want live default", model)
	}

	model = defaultModelFromCatalog([]CodexModel{{ID: "gpt-5"}, {ID: "gpt-5-codex"}})
	if model != "gpt-5" {
		t.Fatalf("model = %q, want first available", model)
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
	s := normalizeSettings(Settings{AppServerProbeSeconds: 1, MaxEventsPerThread: 0, DefaultSandbox: "bogus", DefaultApprovalPolicy: "never"})
	if s.AppServerProbeSeconds < 5 {
		t.Fatalf("probe interval not clamped: %d", s.AppServerProbeSeconds)
	}
	if s.MaxEventsPerThread <= 0 {
		t.Fatalf("max events not defaulted: %d", s.MaxEventsPerThread)
	}
	if s.DefaultSandbox != "read-only" {
		t.Fatalf("invalid sandbox not reset: %s", s.DefaultSandbox)
	}
	if s.DefaultApprovalPolicy != "on-request" {
		t.Fatalf("invalid approval policy not reset: %s", s.DefaultApprovalPolicy)
	}
}

func TestOwnerCommandAssessmentIsControlled(t *testing.T) {
	if assessment, err := assessOwnerCommand([]string{"git", "status", "--short"}); err != nil || assessment.RequiresConfirmation || assessment.NeedsSandbox {
		t.Fatalf("expected read-only git status without confirmation, assessment=%+v err=%v", assessment, err)
	}
	if assessment, err := assessOwnerCommand([]string{"npm", "run", "build"}); err != nil || !assessment.RequiresConfirmation || !assessment.NeedsSandbox {
		t.Fatalf("expected npm build to require confirmation, assessment=%+v err=%v", assessment, err)
	}
	if assessment, err := assessOwnerCommand([]string{"go", "test", "./..."}); err != nil || !assessment.RequiresConfirmation || !assessment.NeedsSandbox {
		t.Fatalf("expected go test to require sandboxed confirmation, assessment=%+v err=%v", assessment, err)
	}
	if _, err := assessOwnerCommand([]string{"git", "branch", "--show-current"}); err != nil {
		t.Fatalf("expected git branch query to be allowed: %v", err)
	}
	for _, argv := range [][]string{{"curl", "https://example.com"}, {"bash", "-lc", "echo hi"}, {"git", "push"}, {"git", "branch", "new-branch"}, {"git", "branch", "-d", "old-branch"}, {"git", "diff", "--no-index", "/etc/passwd", "README.md"}, {"git", "status", "--git-dir=/tmp/repo/.git"}, {"python3", "-c", "print(1)"}} {
		if _, err := assessOwnerCommand(argv); err == nil {
			t.Fatalf("expected command to be blocked: %v", argv)
		}
	}
}

func TestPreviewRewriteProxiesRelativeResources(t *testing.T) {
	body := `<!doctype html><html><head><base href="http://127.0.0.1:5173/"><link rel="stylesheet" href="/assets/app.css"><style>.hero{background:url("./hero.png")}</style></head><body><img src="img/logo.png" srcset="small.png 1x, /big.png 2x"><script src="./app.js"></script></body></html>`
	rewritten := rewritePreviewHTML(body, "http://127.0.0.1:5173/nested/index.html", "/api/codex/browser/sessions/session-1/proxy")
	for _, forbidden := range []string{"<base", `href="/assets/app.css"`, `src="img/logo.png"`, `src="./app.js"`} {
		if strings.Contains(rewritten, forbidden) {
			t.Fatalf("expected %q to be rewritten or removed: %s", forbidden, rewritten)
		}
	}
	for _, required := range []string{
		`/api/codex/browser/sessions/session-1/proxy?url=http%3A%2F%2F127.0.0.1%3A5173%2Fassets%2Fapp.css`,
		`/api/codex/browser/sessions/session-1/proxy?url=http%3A%2F%2F127.0.0.1%3A5173%2Fnested%2Fimg%2Flogo.png`,
		`/api/codex/browser/sessions/session-1/proxy?url=http%3A%2F%2F127.0.0.1%3A5173%2Fnested%2Fapp.js`,
	} {
		if !strings.Contains(rewritten, required) {
			t.Fatalf("expected rewritten preview to contain %q: %s", required, rewritten)
		}
	}
}

func TestActiveForPayloadDoesNotFallbackAcrossConcurrentTurns(t *testing.T) {
	svc := &Service{
		activeTurns:     map[string]*appTurnContext{"turn-1": {threadID: "thread-1", turnID: "turn-1"}, "turn-2": {threadID: "thread-2", turnID: "turn-2"}},
		activeItemTurns: map[string]string{"item-2": "turn-2"},
	}
	if active := svc.activeForPayload(map[string]any{}); active != nil {
		t.Fatalf("expected ambiguous payload to fail closed, got %+v", active)
	}
	if active := svc.activeForPayload(map[string]any{"threadId": "thread-1"}); active == nil || active.turnID != "turn-1" {
		t.Fatalf("expected thread route to turn-1, got %+v", active)
	}
	if active := svc.activeForPayload(map[string]any{"itemId": "item-2"}); active == nil || active.turnID != "turn-2" {
		t.Fatalf("expected item route to turn-2, got %+v", active)
	}
}

func TestNormalizePreviewURLRequiresExplicitPublicAllow(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<html></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, events.NewHub(), dir, func() ([]string, error) { return []string{dir}, nil }, nil)
	ws := storage.CodexCliWorkspace{Path: dir}
	if _, err := svc.normalizePreviewURL(ctx, "https://example.com", ws, false); err == nil {
		t.Fatal("expected public URL without allowPublic to be rejected")
	}
	if _, err := svc.normalizePreviewURL(ctx, "http://127.0.0.1:5173", ws, false); err != nil {
		t.Fatalf("expected localhost preview to be allowed: %v", err)
	}
	if _, err := svc.normalizePreviewURL(ctx, "file://"+indexPath, ws, false); err != nil {
		t.Fatalf("expected workspace file preview to be allowed: %v", err)
	}
	if _, err := svc.normalizePreviewURL(ctx, "https://example.com?access_token=placeholder", ws, true); err == nil {
		t.Fatal("expected sensitive query URL to be rejected")
	}
}

func TestPreviewResourceTransitionBlocksPublicToLocal(t *testing.T) {
	if err := validatePreviewResourceTransition("https://example.com/app", "http://127.0.0.1:5173/assets/app.js"); err == nil {
		t.Fatal("expected public preview to local resource to be blocked")
	}
	if err := validatePreviewResourceTransition("https://example.com/app", "file:///workspace/index.html"); err == nil {
		t.Fatal("expected public preview to file resource to be blocked")
	}
	if err := validatePreviewResourceTransition("https://example.com/app", "https://cdn.example.net/app.js"); err != nil {
		t.Fatalf("expected public preview to public resource to be allowed: %v", err)
	}
	if err := validatePreviewResourceTransition("http://127.0.0.1:5173/app", "http://127.0.0.1:5173/assets/app.js"); err != nil {
		t.Fatalf("expected local preview to local resource to be allowed: %v", err)
	}
}

func TestWorkspaceQueuedTurnBlocksNewStartButNotDispatch(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	workspacePath := filepath.Join(dir, "repo")
	if err := os.Mkdir(workspacePath, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, events.NewHub(), dir, func() ([]string, error) { return []string{dir}, nil }, nil)
	cleanupCodexTestService(t, svc, store)
	ws, err := svc.CreateWorkspaceWithOptions(ctx, storage.CodexCliWorkspace{Path: workspacePath, TrustState: "trusted", DefaultSandbox: "read-only", DefaultApprovalPolicy: "on-request"}, CreateWorkspaceOptions{CreateIfMissing: false})
	if err != nil {
		t.Fatal(err)
	}
	blockingThread, err := store.CreateCodexCliThread(ctx, storage.CodexCliThread{WorkspaceID: ws.ID, Status: "queued", SandboxMode: "read-only", ApprovalPolicy: "on-request"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCodexCliTurn(ctx, storage.CodexCliTurn{ThreadID: blockingThread.ID, Status: "queued", SandboxMode: "read-only", ApprovalPolicy: "on-request"}); err != nil {
		t.Fatal(err)
	}
	if !workspaceHasQueuedOrActiveTurn(ctx, store, ws.ID) {
		t.Fatal("expected queued turn to block a new immediate turn")
	}
	if workspaceHasActiveTurn(ctx, store, ws.ID) {
		t.Fatal("queued turn should not block queue dispatcher as an active turn")
	}
	thread, err := svc.CreateThread(ctx, ws.ID, "next", "", "read-only", "on-request")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := svc.StartTurn(ctx, thread.ID, TurnInput{Prompt: "next"})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Status != "queued" {
		t.Fatalf("expected new turn to queue behind existing workspace turn, got %s", turn.Status)
	}
}

func TestConcurrentStartTurnSerializesWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell stub")
	}
	ctx := context.Background()
	dir := t.TempDir()
	workspacePath := filepath.Join(dir, "repo")
	if err := os.Mkdir(workspacePath, 0o700); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(dir, "codex-stub")
	releasePath := filepath.Join(dir, "release-stub")
	script := "#!/bin/sh\nwhile [ ! -f \"" + releasePath + "\" ]; do sleep 0.05; done\n"
	if err := os.WriteFile(binaryPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, events.NewHub(), dir, func() ([]string, error) { return []string{dir}, nil }, nil)
	cleanupCodexTestService(t, svc, store)
	svc.mu.Lock()
	svc.settings.BinaryPath = binaryPath
	svc.settings.MaxConcurrentTurns = 4
	svc.settings.ExecFallbackEnabled = true
	svc.mu.Unlock()
	ws, err := svc.CreateWorkspaceWithOptions(ctx, storage.CodexCliWorkspace{Path: workspacePath, TrustState: "trusted", DefaultSandbox: "read-only", DefaultApprovalPolicy: "on-request"}, CreateWorkspaceOptions{CreateIfMissing: false})
	if err != nil {
		t.Fatal(err)
	}
	threadIDs := []string{}
	for i := 0; i < 6; i++ {
		thread, err := svc.CreateThread(ctx, ws.ID, "concurrent", "", "read-only", "on-request")
		if err != nil {
			t.Fatal(err)
		}
		threadIDs = append(threadIDs, thread.ID)
	}

	start := make(chan struct{})
	errs := make(chan error, len(threadIDs))
	var wg sync.WaitGroup
	for _, threadID := range threadIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			_, err := svc.StartTurn(ctx, id, TurnInput{Prompt: "hello"})
			errs <- err
		}(threadID)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	running := 0
	queued := 0
	for _, threadID := range threadIDs {
		turns, err := store.ListCodexCliTurns(ctx, threadID)
		if err != nil {
			t.Fatal(err)
		}
		if len(turns) != 1 {
			t.Fatalf("expected one turn for thread %s, got %d", threadID, len(turns))
		}
		switch turns[0].Status {
		case "running":
			running++
		case "queued":
			queued++
		default:
			t.Fatalf("unexpected turn status %q for thread %s", turns[0].Status, threadID)
		}
	}
	if running != 1 || queued != len(threadIDs)-1 {
		t.Fatalf("expected one running turn and the rest queued, got running=%d queued=%d", running, queued)
	}
	if err := os.WriteFile(releasePath, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		counts, err := store.CountCodexCliTurnsByStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if counts["running"] == 0 && counts["queued"] == 0 && counts["waiting_approval"] == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for stub turns to drain: %+v", counts)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestStartTurnInvalidAttachmentDoesNotLeaveRunningTurn(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	workspacePath := filepath.Join(dir, "repo")
	if err := os.Mkdir(workspacePath, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, events.NewHub(), dir, func() ([]string, error) { return []string{dir}, nil }, nil)
	cleanupCodexTestService(t, svc, store)
	ws, err := svc.CreateWorkspaceWithOptions(ctx, storage.CodexCliWorkspace{Path: workspacePath, TrustState: "trusted", DefaultSandbox: "workspace-write", DefaultApprovalPolicy: "on-request"}, CreateWorkspaceOptions{CreateIfMissing: false})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := svc.CreateThread(ctx, ws.ID, "test", "", "read-only", "on-request")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartTurn(ctx, thread.ID, TurnInput{Prompt: "hello", AttachmentIDs: []string{"missing"}}); err == nil {
		t.Fatal("expected invalid attachment to fail")
	}
	runningCount, err := store.CountRunningCodexCliTurns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if runningCount > 0 {
		t.Fatal("invalid attachment left a running turn")
	}
	turns, err := store.ListCodexCliTurns(ctx, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 0 {
		t.Fatalf("expected no turn to be created, got %d", len(turns))
	}
}

func TestResolveApprovalWithoutLiveRequestFailsClosed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, events.NewHub(), dir, func() ([]string, error) { return []string{dir}, nil }, nil)
	cleanupCodexTestService(t, svc, store)
	ws, err := store.CreateCodexCliWorkspace(ctx, storage.CodexCliWorkspace{Path: dir, TrustState: "trusted", DefaultSandbox: "read-only", DefaultApprovalPolicy: "on-request"})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := store.CreateCodexCliThread(ctx, storage.CodexCliThread{WorkspaceID: ws.ID, Status: "needs_approval", SourceMode: "app_server", SandboxMode: "read-only", ApprovalPolicy: "on-request"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := store.CreateCodexCliTurn(ctx, storage.CodexCliTurn{ThreadID: thread.ID, Status: "waiting_approval", SandboxMode: "read-only", ApprovalPolicy: "on-request"})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := store.CreateCodexCliApproval(ctx, storage.CodexCliApproval{ThreadID: thread.ID, TurnID: turn.ID, Status: "pending", ActionKind: "command", RiskLevel: "medium"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveApprovalDecision(ctx, approval.ID, "accept"); !errors.Is(err, ErrApprovalNotLive) {
		t.Fatalf("expected stale approval error, got %v", err)
	}
	resolved, err := store.GetCodexCliApproval(ctx, approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "failed" || resolved.Decision != "stale" {
		t.Fatalf("expected failed stale approval, got %+v", resolved)
	}
	savedTurn, err := store.GetCodexCliTurn(ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if savedTurn.Status != "failed" {
		t.Fatalf("expected waiting turn to fail closed, got %s", savedTurn.Status)
	}
}

func TestTerminalTurnCleansAttachmentsAndDerivesTitle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, events.NewHub(), dir, func() ([]string, error) { return []string{dir}, nil }, nil)
	cleanupCodexTestService(t, svc, store)
	thread, err := store.CreateCodexCliThread(ctx, storage.CodexCliThread{WorkspaceID: "ws-1", Title: "新对话", Status: "running", SourceMode: "exec", SandboxMode: "read-only", ApprovalPolicy: "on-request"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := store.CreateCodexCliTurn(ctx, storage.CodexCliTurn{ThreadID: thread.ID, Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(dir, "input.png")
	if err := os.WriteFile(filePath, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	att, err := store.CreateCodexCliAttachment(ctx, storage.CodexCliAttachment{ThreadID: thread.ID, TurnID: turn.ID, Filename: "input.png", StoragePath: filePath})
	if err != nil {
		t.Fatal(err)
	}
	svc.completeTurn(ctx, thread, turn, nil)
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("expected attachment file removed, stat err=%v", err)
	}
	if _, err := store.GetCodexCliAttachment(ctx, att.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected attachment row removed, err=%v", err)
	}
	if !shouldDeriveThreadTitle("") || !shouldDeriveThreadTitle("新对话") || !shouldDeriveThreadTitle("Untitled") || shouldDeriveThreadTitle("自定义标题") {
		t.Fatal("unexpected title derivation predicate")
	}
	if got := titleFromPrompt("  第一行 prompt\n第二行  "); got != "第一行 prompt 第二行" {
		t.Fatalf("unexpected derived title %q", got)
	}
}

// TestQueuedCommandInterruptedBeforeStartDoesNotRun guards the race where an
// owner interrupts a command while it is still queued: the runner must observe
// the cancelled state and refuse to execute instead of forcing it to running.
func TestQueuedCommandInterruptedBeforeStartDoesNotRun(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	workspacePath := filepath.Join(dir, "repo")
	if err := os.Mkdir(workspacePath, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, events.NewHub(), dir, func() ([]string, error) { return []string{dir}, nil }, nil)
	cleanupCodexTestService(t, svc, store)
	ws, err := svc.CreateWorkspaceWithOptions(ctx, storage.CodexCliWorkspace{Path: workspacePath, TrustState: "trusted", DefaultSandbox: "read-only", DefaultApprovalPolicy: "on-request"}, CreateWorkspaceOptions{CreateIfMissing: false})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := svc.CreateThread(ctx, ws.ID, "cmd", "", "read-only", "on-request")
	if err != nil {
		t.Fatal(err)
	}
	command, err := store.CreateCodexCliCommand(ctx, storage.CodexCliCommand{ThreadID: thread.ID, WorkspaceID: ws.ID, CommandPreview: "git status", Status: "queued"})
	if err != nil {
		t.Fatal(err)
	}

	// Interrupt before the runner starts.
	if _, err := svc.InterruptCommand(ctx, command.ID); err != nil {
		t.Fatal(err)
	}

	// The atomic start must refuse to transition a non-queued command.
	if _, transitioned, err := store.StartCodexCliCommand(ctx, command.ID); err != nil || transitioned {
		t.Fatalf("expected cancelled command to not transition to running, transitioned=%v err=%v", transitioned, err)
	}

	// Running the command body now must be a no-op that leaves it cancelled.
	svc.runOwnerCommand(ctx, command, ws, []string{"git", "status"})
	final, err := store.GetCodexCliCommand(ctx, command.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "cancelled" {
		t.Fatalf("expected command to stay cancelled, got %q", final.Status)
	}
	if final.StartedAt != "" {
		t.Fatalf("expected cancelled command to never start, started_at=%q", final.StartedAt)
	}
}
