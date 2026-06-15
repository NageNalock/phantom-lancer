package probes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---- helpers ---------------------------------------------------------------

func mkTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "probes-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func writeMarker(t *testing.T, path string, m L1Marker) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeFakeMox writes a tiny fake-mox shell script that supports the
// `mox ... config list` subcommand.  Behaviour is controlled by two env
// variables set by tests:
//
//	FAKEMOX_CONFIG_STDOUT    – bytes written to stdout
//	FAKEMOX_CONFIG_EXITCODE  – exit code (default 0)
func writeFakeMox(t *testing.T, dir string) string {
	t.Helper()
	script := `#!/bin/sh
found=0
for arg in "$@"; do
  [ "$arg" = "list" ] && found=1
done
if [ "$found" = "1" ]; then
  printf '%s' "$FAKEMOX_CONFIG_STDOUT"
  exit ${FAKEMOX_CONFIG_EXITCODE:-0}
fi
exit 0
`
	path := filepath.Join(dir, "mox")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---- Severity --------------------------------------------------------------

func TestSeverityString(t *testing.T) {
	cases := map[Severity]string{
		StateUnknown: "unknown",
		StateGreen:   "green",
		StateYellow:  "yellow",
		StateRed:     "red",
		Severity(99): "severity(99)",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Severity(%d).String() = %q, want %q", int(s), got, want)
		}
	}
}

func TestSeverityMarshalJSON(t *testing.T) {
	b, err := StateYellow.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"yellow"` {
		t.Errorf("marshal = %s, want \"yellow\"", b)
	}
}

func TestSummary(t *testing.T) {
	cases := []struct {
		name   string
		states []Severity
		want   Severity
	}{
		{"empty", nil, StateGreen},
		{"all green", []Severity{StateGreen, StateGreen}, StateGreen},
		{"yellow wins", []Severity{StateGreen, StateYellow}, StateYellow},
		{"red wins over yellow", []Severity{StateYellow, StateRed}, StateRed},
		{"unknown degrades to yellow", []Severity{StateGreen, StateUnknown}, StateYellow},
		{"red wins everything", []Severity{StateUnknown, StateYellow, StateRed}, StateRed},
	}
	for _, tc := range cases {
		rs := make([]Result, len(tc.states))
		for i, s := range tc.states {
			rs[i].State = s
		}
		if got := Summary(rs); got != tc.want {
			t.Errorf("%s: Summary = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// ---- L1Process --------------------------------------------------------------

func TestL1_NoMarker_Unknown(t *testing.T) {
	dir := mkTempDir(t)
	p := NewL1Process(L1Config{MarkerPath: filepath.Join(dir, "nonexistent", "marker.json")})
	r := p.Run(context.Background())
	if r.State != StateUnknown {
		t.Errorf("no marker → state=%s, want unknown (msg=%q)", r.State, r.Message)
	}
}

func TestL1_EmptyConfig_Unknown(t *testing.T) {
	p := NewL1Process(L1Config{})
	r := p.Run(context.Background())
	if r.State != StateUnknown {
		t.Errorf("empty cfg → state=%s, want unknown", r.State)
	}
}

func TestL1_MarkerMissingPID_Red(t *testing.T) {
	dir := mkTempDir(t)
	mp := filepath.Join(dir, "run", "marker")
	writeMarker(t, mp, L1Marker{Version: 1, PID: 0})
	p := NewL1Process(L1Config{MarkerPath: mp})
	r := p.Run(context.Background())
	if r.State != StateRed {
		t.Errorf("bad pid → state=%s, want red (msg=%q)", r.State, r.Message)
	}
}

func TestL1_StalePID_NotGreen(t *testing.T) {
	// Use a PID that is almost certainly dead (way above linux pid_max).
	// It's fine if signal(0) returns EPERM or ESRCH or Yellow – only Green is wrong.
	dir := mkTempDir(t)
	mp := filepath.Join(dir, "marker")
	writeMarker(t, mp, L1Marker{Version: 1, PID: 999999})
	p := NewL1Process(L1Config{MarkerPath: mp})
	r := p.Run(context.Background())
	if r.State == StateGreen {
		t.Errorf("stale pid 999999 → state Green, impossible (msg=%q)", r.Message)
	}
}

func TestL1_AliveOurPID_NotRed(t *testing.T) {
	// Our own process IS alive – we at least should not see Red.
	// On macOS (no /proc starttime) we may get Yellow or Green; on Linux, Green.
	dir := mkTempDir(t)
	mp := filepath.Join(dir, "marker")
	writeMarker(t, mp, L1Marker{
		Version:         1,
		PID:             os.Getpid(),
		BootID:          "boot-abc123",
		BinaryPath:      "/usr/bin/mox",
		PhantomInstance: "phantom-1",
	})
	p := NewL1Process(L1Config{
		MarkerPath:         mp,
		ExpectedBinaryPath: "/usr/bin/mox",
		ExpectedInstance:   "phantom-1",
	})
	r := p.Run(context.Background())
	if r.State == StateRed {
		t.Errorf("our own pid → state Red, unexpected (msg=%q)", r.Message)
	}
}

func TestL1_InstanceMismatch_NotGreen(t *testing.T) {
	dir := mkTempDir(t)
	mp := filepath.Join(dir, "marker")
	writeMarker(t, mp, L1Marker{
		Version:         1,
		PID:             os.Getpid(),
		BinaryPath:      "/usr/bin/mox",
		PhantomInstance: "different-instance",
	})
	p := NewL1Process(L1Config{
		MarkerPath:       mp,
		ExpectedInstance: "our-instance",
	})
	r := p.Run(context.Background())
	if r.State == StateGreen {
		t.Errorf("instance mismatch → Green, unexpected (msg=%q)", r.Message)
	}
}

// ---- L2Control --------------------------------------------------------------

func TestL2_EmptyConfig_Unknown(t *testing.T) {
	p := NewL2Control(L2Config{})
	r := p.Run(context.Background())
	if r.State != StateUnknown {
		t.Errorf("empty cfg → state=%s, want unknown", r.State)
	}
}

func TestL2_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-mox shell script not portable")
	}
	dir := mkTempDir(t)
	bin := writeFakeMox(t, dir)
	cfgPath := filepath.Join(dir, "mox.conf")
	dataDir := filepath.Join(dir, "data")
	_ = os.MkdirAll(dataDir, 0o700)
	_ = os.WriteFile(cfgPath, []byte("domains: []\n"), 0o600)
	t.Setenv("FAKEMOX_CONFIG_STDOUT", "example.com\nfoo.example\n")
	t.Setenv("FAKEMOX_CONFIG_EXITCODE", "0")

	p := NewL2Control(L2Config{
		BinaryPath: bin, ConfigPath: cfgPath, DataDir: dataDir,
	})
	r := p.Run(context.Background())
	if r.State != StateGreen {
		t.Errorf("state = %s, want green (msg=%q, err=%v)", r.State, r.Message, r.Err)
	}
	if r.Duration <= 0 {
		t.Errorf("Duration not recorded")
	}
	if r.Name != "l2_control" || r.Layer != 2 {
		t.Errorf("name/layer mismatch: %+v", r)
	}
}

func TestL2_ExitNonZero_Red(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-mox shell script not portable")
	}
	dir := mkTempDir(t)
	bin := writeFakeMox(t, dir)
	cfgPath := filepath.Join(dir, "mox.conf")
	dataDir := filepath.Join(dir, "data")
	_ = os.MkdirAll(dataDir, 0o700)
	t.Setenv("FAKEMOX_CONFIG_STDOUT", "")
	t.Setenv("FAKEMOX_CONFIG_EXITCODE", "2")

	p := NewL2Control(L2Config{
		BinaryPath: bin, ConfigPath: cfgPath, DataDir: dataDir,
	})
	r := p.Run(context.Background())
	if r.State != StateRed {
		t.Errorf("non-zero exit → state=%s, want red (msg=%q)", r.State, r.Message)
	}
}

// ---- L3WebAPI --------------------------------------------------------------

func TestL3_EmptyURL_Unknown(t *testing.T) {
	p := NewL3WebAPI(L3Config{})
	r := p.Run(context.Background())
	if r.State != StateUnknown {
		t.Errorf("empty url → %s, want unknown", r.State)
	}
}

func TestL3_200OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = fmt.Fprintln(w, "hi")
	}))
	defer srv.Close()
	p := NewL3WebAPI(L3Config{BaseURL: srv.URL, Timeout: 3 * time.Second})
	r := p.Run(context.Background())
	if r.State != StateGreen {
		t.Errorf("200 → state=%s, want green (msg=%q)", r.State, r.Message)
	}
}

func TestL3_401Unauth_Green(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = fmt.Fprintln(w, "auth required")
	}))
	defer srv.Close()
	p := NewL3WebAPI(L3Config{BaseURL: srv.URL, Timeout: 3 * time.Second})
	r := p.Run(context.Background())
	if r.State != StateGreen {
		t.Errorf("401 → state=%s, want green (msg=%q)", r.State, r.Message)
	}
}

func TestL3_500_Red(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	p := NewL3WebAPI(L3Config{BaseURL: srv.URL, Timeout: 3 * time.Second})
	r := p.Run(context.Background())
	if r.State != StateRed {
		t.Errorf("500 → state=%s, want red (msg=%q)", r.State, r.Message)
	}
}

func TestL3_ConnRefused_Red(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	time.Sleep(10 * time.Millisecond)

	p := NewL3WebAPI(L3Config{BaseURL: "http://" + addr, Timeout: 1 * time.Second})
	r := p.Run(context.Background())
	if r.State != StateRed {
		t.Errorf("conn refused → state=%s, want red (msg=%q)", r.State, r.Message)
	}
}

func TestL3_RequireStatusCodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()

	p := NewL3WebAPI(L3Config{
		BaseURL:            srv.URL,
		Timeout:            3 * time.Second,
		RequireStatusCodes: []int{200},
	})
	r := p.Run(context.Background())
	if r.State != StateYellow {
		t.Errorf("204 not in [200] → state=%s, want yellow (msg=%q)", r.State, r.Message)
	}

	p2 := NewL3WebAPI(L3Config{
		BaseURL:            srv.URL,
		Timeout:            3 * time.Second,
		RequireStatusCodes: []int{200, 204},
	})
	r2 := p2.Run(context.Background())
	if r2.State != StateGreen {
		t.Errorf("204 in [200,204] → state=%s, want green (msg=%q)", r2.State, r2.Message)
	}
}

func TestL3_UnixSocket(t *testing.T) {
	dir := mkTempDir(t)
	sock := filepath.Join(dir, "test.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			buf := make([]byte, 4096)
			_, _ = conn.Read(buf)
			_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"))
			_ = conn.Close()
		}
	}()

	p := NewL3WebAPI(L3Config{
		BaseURL:        "http://dummy-host.local/",
		DialUnixSocket: sock,
		Timeout:        3 * time.Second,
	})
	r := p.Run(context.Background())
	if r.State != StateGreen {
		t.Errorf("unix sock → state=%s, want green (msg=%q)", r.State, r.Message)
	}
}

func TestL3_CancelledContext_NotGreen(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(10 * time.Second)
		w.WriteHeader(200)
	}))
	srv.Start()
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	p := NewL3WebAPI(L3Config{BaseURL: srv.URL, Timeout: 10 * time.Second})
	r := p.Run(ctx)
	if r.State == StateGreen {
		t.Errorf("cancelled ctx → state Green, impossible (msg=%q)", r.Message)
	}
}

// ---- readProcStartTimeTicks (Linux only) ---------------------------------

func TestReadProcStartTimeTicks_Parse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	ourPID := os.Getpid()
	v, src, ok := readProcStartTimeTicks(ourPID)
	if !ok {
		t.Fatalf("could not read our own /proc/%d/stat: %s", ourPID, src)
	}
	if v == 0 {
		t.Errorf("starttime == 0 for our own pid")
	}
}

// ---- RunAll concurrent cap ------------------------------------------------

type slowProbe struct {
	delay time.Duration
	name  string
}

func (s *slowProbe) Name() string { return s.name }
func (s *slowProbe) Layer() int { return 99 }
func (s *slowProbe) Run(ctx context.Context) Result {
	start := time.Now()
	select {
	case <-ctx.Done():
		return Result{Name: s.Name(), Layer: s.Layer(), State: StateRed,
			StartedAt: start, Duration: time.Since(start), Message: ctx.Err().Error()}
	case <-time.After(s.delay):
	}
	return Result{Name: s.Name(), Layer: s.Layer(), State: StateGreen,
		StartedAt: start, Duration: time.Since(start), Message: "ok"}
}

func TestRunAll_ConcurrencyCap(t *testing.T) {
	start := time.Now()
	probes := []Probe{
		&slowProbe{name: "a", delay: 50 * time.Millisecond},
		&slowProbe{name: "b", delay: 50 * time.Millisecond},
		&slowProbe{name: "c", delay: 50 * time.Millisecond},
		&slowProbe{name: "d", delay: 50 * time.Millisecond},
		&slowProbe{name: "e", delay: 50 * time.Millisecond},
		&slowProbe{name: "f", delay: 50 * time.Millisecond},
	}
	results := RunAll(context.Background(), probes)
	elapsed := time.Since(start)

	if len(results) != len(probes) {
		t.Fatalf("len(results) = %d, want %d", len(results), len(probes))
	}

	// With cap=3 we should see ~6*50ms/3 = 100ms.  Add a generous margin.
	// The minimum is max(100ms) and the theoretical ceiling is < 200ms (2 waves).
	if elapsed >= 500*time.Millisecond {
		t.Errorf("6 probes took %v, expected ≤ ~200ms (two waves)", elapsed)
	}
	for i, r := range results {
		if r.Name == "" || r.Layer == 0 {
			t.Errorf("result %d: missing Name/Layer in %+v", i, r)
		}
	}
}

// ---- Result invariants ----------------------------------------------------

func TestResultFields_Populated(t *testing.T) {
	p := NewL2Control(L2Config{})
	r := p.Run(context.Background())
	if r.StartedAt.IsZero() {
		t.Error("StartedAt not set")
	}
	if r.Duration < 0 {
		t.Errorf("Duration negative: %v", r.Duration)
	}
	if r.Name == "" {
		t.Error("Name missing")
	}
}

// Ensure Result.Err is excluded from JSON (so it never leaks to UI consumers).
func TestResultErr_ExcludedFromJSON(t *testing.T) {
	r := Result{
		Name:      "x",
		State:     StateRed,
		Message:   "sanitised",
		StartedAt: time.Now(),
		Duration:  42 * time.Millisecond,
		Err:       fmt.Errorf("private detail: secret=abc123"),
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "secret=abc123") {
		t.Errorf("Result.Err leaked into JSON: %s", s)
	}
	if !strings.Contains(s, "sanitised") {
		t.Errorf("public Message missing from JSON: %s", s)
	}
}

// keep bytes import (used in probe implementations; guards against accidental
// removal in a future refactor that would otherwise break the import list).
var _ = bytes.Contains
