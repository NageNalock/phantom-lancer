package config

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	ConfigPath            string
	Addr                  string
	DataDir               string
	DBPath                string
	LogFile               string
	LogMaxSizeMB          int
	LogMaxFiles           int
	LogMaxAgeDays         int
	LogStdout             bool
	AllowedRoots          []string
	CodexBinary           string
	CodexHome             string
	CookieSecure          bool
	LoginFailureThreshold int
}

func Load(args []string) (Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return Config{}, err
	}

	cfg := defaults(cwd)
	configPath, configRequired := configPathFrom(cwd, args)
	if configPath != "" {
		if err := applyConfigFile(&cfg, configPath); err != nil {
			return Config{}, err
		}
		cfg.ConfigPath = configPath
	} else if configRequired {
		return Config{}, fmt.Errorf("config file not found: %s", configPath)
	}

	applyEnv(&cfg)

	fs := flag.NewFlagSet("phantom-lancer", flag.ContinueOnError)
	fs.StringVar(&cfg.ConfigPath, "config", cfg.ConfigPath, "TOML config file path")
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "HTTP listen address")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "application data directory")
	fs.StringVar(&cfg.DBPath, "db", cfg.DBPath, "SQLite database path")
	fs.StringVar(&cfg.LogFile, "log-file", cfg.LogFile, "managed JSONL service log path")
	fs.IntVar(&cfg.LogMaxSizeMB, "log-max-size-mb", cfg.LogMaxSizeMB, "managed service log max size before rotation")
	fs.IntVar(&cfg.LogMaxFiles, "log-max-files", cfg.LogMaxFiles, "managed service log rotated file count to retain")
	fs.IntVar(&cfg.LogMaxAgeDays, "log-max-age-days", cfg.LogMaxAgeDays, "managed service log max age in days")
	fs.BoolVar(&cfg.LogStdout, "log-stdout", cfg.LogStdout, "also write structured service logs to stdout")
	fs.StringVar(&cfg.CodexBinary, "codex", cfg.CodexBinary, "default codex CLI binary path")
	fs.StringVar(&cfg.CodexHome, "codex-home", cfg.CodexHome, "default CODEX_HOME")
	fs.IntVar(&cfg.LoginFailureThreshold, "login-failure-threshold", cfg.LoginFailureThreshold, "failed login attempts before account/IP backoff")
	allowedRoots := fs.String("allowed-roots", strings.Join(cfg.AllowedRoots, ","), "comma-separated default workspace roots")
	cookieSecure := fs.Bool("cookie-secure", cfg.CookieSecure, "default Secure flag for session cookies")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	cfg.AllowedRoots = splitList(*allowedRoots)
	cfg.CookieSecure = *cookieSecure

	baseDir := cwd
	if cfg.ConfigPath != "" {
		if abs, err := filepath.Abs(cfg.ConfigPath); err == nil {
			cfg.ConfigPath = abs
			baseDir = filepath.Dir(abs)
		}
	}
	cfg.DataDir = resolvePath(baseDir, cfg.DataDir)
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.DataDir, "phantom.db")
	} else {
		cfg.DBPath = resolvePath(baseDir, cfg.DBPath)
	}
	if cfg.LogFile == "" {
		cfg.LogFile = filepath.Join(cfg.DataDir, "logs", "phantom-lancer.jsonl")
	} else {
		cfg.LogFile = resolvePath(baseDir, cfg.LogFile)
	}
	cfg.CodexHome = resolvePath(baseDir, cfg.CodexHome)
	for index, root := range cfg.AllowedRoots {
		cfg.AllowedRoots[index] = resolvePath(baseDir, root)
	}

	if cfg.Addr == "" {
		return Config{}, errors.New("addr is required")
	}
	if len(cfg.AllowedRoots) == 0 {
		return Config{}, errors.New("at least one default allowed root is required")
	}
	if cfg.LoginFailureThreshold <= 0 {
		return Config{}, errors.New("login failure threshold must be positive")
	}
	if cfg.LogMaxSizeMB <= 0 {
		return Config{}, errors.New("log max size must be positive")
	}
	if cfg.LogMaxFiles < 0 {
		return Config{}, errors.New("log max files cannot be negative")
	}
	if cfg.LogMaxAgeDays < 0 {
		return Config{}, errors.New("log max age days cannot be negative")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaults(cwd string) Config {
	return Config{
		Addr:                  "127.0.0.1:8080",
		DataDir:               filepath.Join(cwd, ".phantom-data"),
		AllowedRoots:          []string{cwd},
		CodexBinary:           "codex",
		LogMaxSizeMB:          32,
		LogMaxFiles:           5,
		LogMaxAgeDays:         14,
		LoginFailureThreshold: 5,
	}
}

func configPathFrom(cwd string, args []string) (string, bool) {
	if value := os.Getenv("PL_CONFIG"); value != "" {
		return resolvePath(cwd, value), true
	}
	for index, arg := range args {
		if arg == "--config" && index+1 < len(args) {
			return resolvePath(cwd, args[index+1]), true
		}
		if strings.HasPrefix(arg, "--config=") {
			return resolvePath(cwd, strings.TrimPrefix(arg, "--config=")), true
		}
	}
	defaultPath := filepath.Join(cwd, "configs", "phantom.toml")
	if _, err := os.Stat(defaultPath); err == nil {
		return defaultPath, false
	}
	return "", false
}

func applyEnv(cfg *Config) {
	if value := os.Getenv("PL_ADDR"); value != "" {
		cfg.Addr = value
	}
	if value := os.Getenv("PL_DATA_DIR"); value != "" {
		cfg.DataDir = value
	}
	if value := os.Getenv("PL_DB_PATH"); value != "" {
		cfg.DBPath = value
	}
	if value := os.Getenv("PL_SERVICE_LOG_FILE"); value != "" {
		cfg.LogFile = value
	}
	if value := os.Getenv("PL_LOG_MAX_SIZE_MB"); value != "" {
		cfg.LogMaxSizeMB = parseInt(value, cfg.LogMaxSizeMB)
	}
	if value := os.Getenv("PL_LOG_MAX_FILES"); value != "" {
		cfg.LogMaxFiles = parseInt(value, cfg.LogMaxFiles)
	}
	if value := os.Getenv("PL_LOG_MAX_AGE_DAYS"); value != "" {
		cfg.LogMaxAgeDays = parseInt(value, cfg.LogMaxAgeDays)
	}
	if value := os.Getenv("PL_LOG_STDOUT"); value != "" {
		cfg.LogStdout = parseBool(value)
	}
	if value := os.Getenv("PL_ALLOWED_ROOTS"); value != "" {
		cfg.AllowedRoots = splitList(value)
	}
	if value := os.Getenv("PL_CODEX_BINARY"); value != "" {
		cfg.CodexBinary = value
	}
	if value := os.Getenv("PL_CODEX_HOME"); value != "" {
		cfg.CodexHome = value
	} else if value := os.Getenv("CODEX_HOME"); value != "" {
		cfg.CodexHome = value
	}
	if value := os.Getenv("PL_COOKIE_SECURE"); value != "" {
		cfg.CookieSecure = parseBool(value)
	}
	if value := os.Getenv("PL_LOGIN_FAILURE_THRESHOLD"); value != "" {
		cfg.LoginFailureThreshold = parseInt(value, cfg.LoginFailureThreshold)
	}
}

func applyConfigFile(cfg *Config, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	section := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid config line in %s: %s", path, line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		fullKey := key
		if section != "" {
			fullKey = section + "." + key
		}
		switch fullKey {
		case "server.addr":
			cfg.Addr = parseString(value)
		case "storage.data_dir", "paths.data_dir":
			cfg.DataDir = parseString(value)
		case "storage.sqlite_path", "storage.db_path":
			cfg.DBPath = parseString(value)
		case "logging.file_path", "logging.log_file":
			cfg.LogFile = parseString(value)
		case "logging.max_size_mb":
			cfg.LogMaxSizeMB = parseInt(value, cfg.LogMaxSizeMB)
		case "logging.max_files":
			cfg.LogMaxFiles = parseInt(value, cfg.LogMaxFiles)
		case "logging.max_age_days":
			cfg.LogMaxAgeDays = parseInt(value, cfg.LogMaxAgeDays)
		case "logging.stdout":
			cfg.LogStdout = parseBool(value)
		case "bootstrap.allowed_roots", "runtime.allowed_roots", "paths.allowed_roots":
			cfg.AllowedRoots = parseStringArray(value)
		case "bootstrap.codex_binary", "runtime.codex_binary", "codex.binary_path":
			cfg.CodexBinary = parseString(value)
		case "bootstrap.codex_home", "runtime.codex_home", "codex.codex_home":
			cfg.CodexHome = parseString(value)
		case "bootstrap.cookie_secure", "runtime.cookie_secure", "security.cookie_secure":
			cfg.CookieSecure = parseBool(value)
		case "auth.login_failure_threshold", "security.login_failure_threshold":
			cfg.LoginFailureThreshold = parseInt(value, cfg.LoginFailureThreshold)
		}
	}
	return scanner.Err()
}

func stripComment(line string) string {
	inQuote := false
	escaped := false
	for index, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if r == '#' && !inQuote {
			return line[:index]
		}
	}
	return line
}

func parseString(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return strings.TrimSuffix(strings.TrimPrefix(value, `"`), `"`)
	}
	return value
}

func parseStringArray(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(parseString(part)); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func resolvePath(baseDir, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	if strings.HasPrefix(value, "~") {
		return value
	}
	return filepath.Clean(filepath.Join(baseDir, value))
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
