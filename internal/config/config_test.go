package config

import (
	"path/filepath"
	"testing"
)

func TestLoginFailureThresholdDefaultAndEnv(t *testing.T) {
	cfg := defaults("/tmp/phantom-lancer")
	if cfg.LoginFailureThreshold != 5 {
		t.Fatalf("default LoginFailureThreshold = %d, want 5", cfg.LoginFailureThreshold)
	}
	if cfg.LogMaxSizeMB != 32 || cfg.LogMaxFiles != 5 || cfg.LogMaxAgeDays != 14 {
		t.Fatalf("default log rotation = %d/%d/%d, want 32/5/14", cfg.LogMaxSizeMB, cfg.LogMaxFiles, cfg.LogMaxAgeDays)
	}
	if !cfg.Pprof.Enabled || cfg.Pprof.Addr != "127.0.0.1:6060" {
		t.Fatalf("default pprof = %+v, want enabled 127.0.0.1:6060", cfg.Pprof)
	}

	t.Setenv("PL_LOGIN_FAILURE_THRESHOLD", "7")
	t.Setenv("PL_LOG_MAX_SIZE_MB", "64")
	t.Setenv("PL_LOG_MAX_FILES", "8")
	t.Setenv("PL_LOG_MAX_AGE_DAYS", "21")
	t.Setenv("PL_LOG_STDOUT", "true")
	t.Setenv("PL_PPROF_ENABLED", "true")
	t.Setenv("PL_PPROF_ADDR", "localhost:6061")
	applyEnv(&cfg)
	if cfg.LoginFailureThreshold != 7 {
		t.Fatalf("env LoginFailureThreshold = %d, want 7", cfg.LoginFailureThreshold)
	}
	if cfg.LogMaxSizeMB != 64 || cfg.LogMaxFiles != 8 || cfg.LogMaxAgeDays != 21 || !cfg.LogStdout {
		t.Fatalf("env log settings = %d/%d/%d stdout=%v, want 64/8/21 true", cfg.LogMaxSizeMB, cfg.LogMaxFiles, cfg.LogMaxAgeDays, cfg.LogStdout)
	}
	if !cfg.Pprof.Enabled || cfg.Pprof.Addr != "localhost:6061" {
		t.Fatalf("env pprof = %+v, want enabled localhost:6061", cfg.Pprof)
	}
}

func TestLogFileDefaultsUnderDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load([]string{"--data-dir", dir})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "logs", "phantom-lancer.jsonl")
	if cfg.LogFile != want {
		t.Fatalf("LogFile = %q, want %q", cfg.LogFile, want)
	}
}

func TestPprofRejectsNonLoopbackAddr(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load([]string{"--data-dir", dir, "--pprof-enabled", "--pprof-addr", "0.0.0.0:6060"}); err == nil {
		t.Fatal("Load accepted non-loopback pprof addr")
	}
}
