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

	t.Setenv("PL_LOGIN_FAILURE_THRESHOLD", "7")
	t.Setenv("PL_LOG_MAX_SIZE_MB", "64")
	t.Setenv("PL_LOG_MAX_FILES", "8")
	t.Setenv("PL_LOG_MAX_AGE_DAYS", "21")
	t.Setenv("PL_LOG_STDOUT", "true")
	applyEnv(&cfg)
	if cfg.LoginFailureThreshold != 7 {
		t.Fatalf("env LoginFailureThreshold = %d, want 7", cfg.LoginFailureThreshold)
	}
	if cfg.LogMaxSizeMB != 64 || cfg.LogMaxFiles != 8 || cfg.LogMaxAgeDays != 21 || !cfg.LogStdout {
		t.Fatalf("env log settings = %d/%d/%d stdout=%v, want 64/8/21 true", cfg.LogMaxSizeMB, cfg.LogMaxFiles, cfg.LogMaxAgeDays, cfg.LogStdout)
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
