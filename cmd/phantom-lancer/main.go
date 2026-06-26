package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"phantom-lancer/internal/buildinfo"
	"phantom-lancer/internal/codexclient"
	"phantom-lancer/internal/codexgateway"
	"phantom-lancer/internal/config"
	"phantom-lancer/internal/dockercontrol"
	"phantom-lancer/internal/events"
	"phantom-lancer/internal/httpapi"
	"phantom-lancer/internal/httpserver"
	"phantom-lancer/internal/images"
	"phantom-lancer/internal/logging"
	logcenter "phantom-lancer/internal/logs"
	"phantom-lancer/internal/selfupdate"
	stockv2svc "phantom-lancer/internal/stockv2"
	"phantom-lancer/internal/storage"
	"phantom-lancer/internal/v2ray"
	"phantom-lancer/web"
)

func main() {
	if wantsVersion(os.Args[1:]) {
		fmt.Printf("phantom-lancer %s commit=%s date=%s %s/%s\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date, runtime.GOOS, runtime.GOARCH)
		return
	}

	bootstrapLogger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		bootstrapLogger.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logHandle, err := logging.NewLogger(logging.Config{
		Path:        cfg.LogFile,
		MaxSizeMB:   cfg.LogMaxSizeMB,
		MaxFiles:    cfg.LogMaxFiles,
		MaxAgeDays:  cfg.LogMaxAgeDays,
		WriteStdout: cfg.LogStdout,
	})
	if err != nil {
		bootstrapLogger.Error("initialize logging failed", "error", err)
		os.Exit(1)
	}
	defer logHandle.Close()
	logger := logHandle.Logger
	logger.Info(
		"phantom lancer boot starting",
		"version", buildinfo.Version,
		"commit", buildinfo.Commit,
		"addr", cfg.Addr,
		"config", cfg.ConfigPath,
		"data_dir", cfg.DataDir,
		"db", cfg.DBPath,
		"log_file", cfg.LogFile,
		"updates_enabled", cfg.Updates.Enabled,
		"updates_restart_mode", cfg.Updates.RestartMode,
		"updates_install_binary_path", cfg.Updates.InstallBinaryPath,
	)

	ctx := context.Background()
	store, err := storage.Open(ctx, cfg.DBPath, logger)
	if err != nil {
		logger.Error("open storage failed", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.EnsureRuntimeSettings(ctx, storage.RuntimeSettings{
		AllowedRoots: cfg.AllowedRoots,
		CookieSecure: cfg.CookieSecure,
		Addr:         cfg.Addr,
	}); err != nil {
		logger.Error("initialize runtime settings failed", "error", err)
		os.Exit(1)
	}
	effectiveAddr := cfg.Addr
	if initialRuntime, err := store.GetRuntimeSettings(ctx); err != nil {
		logger.Error("load runtime settings failed", "error", err)
		os.Exit(1)
	} else if initialRuntime.Addr != "" {
		effectiveAddr = initialRuntime.Addr
	}

	staticFS, err := fs.Sub(web.Files, "dist")
	if err != nil {
		logger.Error("load embedded web assets failed", "error", err)
		os.Exit(1)
	}

	hub := events.NewHub()
	codexGatewaySvc := codexgateway.NewService(store, logger)
	codexSvc := codexclient.NewService(store, hub, cfg.DataDir, func() ([]string, error) {
		settings, err := store.GetRuntimeSettings(ctx)
		if err != nil {
			return nil, err
		}
		return settings.AllowedRoots, nil
	}, logger)
	defer codexSvc.Close()
	defer codexGatewaySvc.Close()
	v2raySvc := v2ray.NewService(store, hub, cfg.DataDir, logger)
	defer v2raySvc.Close()
	imagesSvc := images.NewService(store, hub, cfg.DataDir, logger)
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}
	stockV2Store, err := stockv2svc.NewStoreWithMarketDB(
		cfg.DBPath,
		stockv2svc.DefaultMarketDBPath(cfg.DataDir, cfg.DBPath),
	)
	if err != nil {
		logger.Error("open stockv2 store failed", "error", err)
		os.Exit(1)
	}
	stockV2Svc := stockv2svc.NewService(stockV2Store, logger, httpClient)
	defer stockV2Svc.Close()
	if err := stockV2Svc.Initialize(ctx); err != nil {
		logger.Error("initialize stock v2 service failed", "error", err)
		os.Exit(1)
	}
	dockerSvc := dockercontrol.NewService(store, hub, cfg.DataDir, logger)
	defer dockerSvc.Close()
	logsSvc := logcenter.NewService(store, cfg)
	restartRequested := make(chan struct{}, 1)
	var selfExecPath string
	updateSvc := selfupdate.NewService(store, hub, logger, selfupdate.Config{
		Enabled:           cfg.Updates.Enabled,
		Repository:        cfg.Updates.Repository,
		AssetName:         cfg.Updates.AssetName,
		RestartMode:       cfg.Updates.RestartMode,
		InstallBinaryPath: cfg.Updates.InstallBinaryPath,
		DataDir:           cfg.DataDir,
		BackupRetention:   cfg.Updates.BackupRetention,
		DownloadTimeout:   time.Duration(cfg.Updates.DownloadTimeoutSeconds) * time.Second,
		RestartTimeout:    time.Duration(cfg.Updates.RestartTimeoutSeconds) * time.Second,
		Build:             buildinfo.Current(),
		RequestRestart: func() {
			logger.Info("self-update restart signal received", "restart_mode", cfg.Updates.RestartMode)
			select {
			case restartRequested <- struct{}{}:
			default:
				logger.Warn("self-update restart signal dropped because restart is already pending", "restart_mode", cfg.Updates.RestartMode)
			}
		},
		PrepareSelfExec: func(p string) {
			selfExecPath = filepath.Clean(p)
			logger.Info("self-update prepared self-exec path", "path", selfExecPath)
		},
	})
	rollbackPath := updateSvc.ConfirmBoot(ctx)
	if rollbackPath != "" {
		logger.Info("self-update watchdog triggered, rolling back to backup binary", "path", rollbackPath)
		logger.Info("phantom lancer preparing rollback self-exec", "path", rollbackPath)
		orderlyClose(logger, dockerSvc, stockV2Svc, v2raySvc, codexGatewaySvc, codexSvc, store, logHandle)
		if err := syscall.Exec(rollbackPath, os.Args, os.Environ()); err != nil {
			logger.Error("rollback syscall.Exec failed", "error", err, "path", rollbackPath)
			os.Exit(1)
		}
	}
	if err := codexGatewaySvc.Ensure(ctx); err != nil {
		logger.Error("initialize codex gateway failed", "error", err)
		os.Exit(1)
	}
	if err := imagesSvc.Ensure(ctx); err != nil {
		logger.Error("initialize images settings failed", "error", err)
		os.Exit(1)
	}
	if err := v2raySvc.Ensure(ctx); err != nil {
		logger.Error("initialize v2ray settings failed", "error", err)
		os.Exit(1)
	}
	if err := codexSvc.Ensure(ctx); err != nil {
		logger.Error("initialize codex client failed", "error", err)
		os.Exit(1)
	}
	if err := updateSvc.Ensure(ctx); err != nil {
		logger.Error("initialize self-update stale-job cleanup failed", "error", err)
		os.Exit(1)
	}
	codexSvc.StartBackground(ctx)
	codexGatewaySvc.StartBackground(ctx)
	store.StartStatsCollector(ctx)

	// Events table retention pruner: deletes events older than the
	// configured window. Runs once shortly after boot, then daily.
	go func() {
		timer := time.NewTimer(2 * time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				days := store.GetEventRetentionDays(ctx)
				if days <= 0 {
					timer.Reset(24 * time.Hour)
					continue
				}
				cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
				removed, err := store.DeleteEventsOlderThan(ctx, cutoff, 0)
				if err != nil {
					logger.Warn("event retention cleanup failed",
						"error", err, "retention_days", days)
				} else if removed > 0 {
					logger.Info("event retention cleanup completed",
						"removed", removed, "retention_days", days,
						"cutoff", cutoff.Format(time.RFC3339))
				}
				timer.Reset(24 * time.Hour)
			case <-ctx.Done():
				return
			}
		}
	}()
	if v2raySettings, err := store.GetV2RaySettings(ctx); err == nil && v2raySettings.Enabled && v2raySettings.StartOnPhantomLaunch {
		if _, err := v2raySvc.Start(ctx); err != nil {
			logger.Error("start embedded v2ray failed", "error", err)
		}
	}
	api, err := httpapi.New(cfg, store, hub, codexGatewaySvc, codexSvc, v2raySvc, imagesSvc, stockV2Svc, dockerSvc, logsSvc, updateSvc, staticFS, logger)
	if err != nil {
		logger.Error("create api failed", "error", err)
		os.Exit(1)
	}

	// PL_TLS_BOOT_STRICT=true/false controls boot-fallback behaviour when TLS
	// is configured in the DB but certs/key cannot be loaded.  Default (false)
	// = fall back to HTTP on the same address and reconcile the DB.
	tlsBootStrict := strings.EqualFold(os.Getenv("PL_TLS_BOOT_STRICT"), "true") || os.Getenv("PL_TLS_BOOT_STRICT") == "1"
	api.SetTLSEnvironmentInfo(tlsBootStrict)

	initialRuntime, err := store.GetRuntimeSettings(ctx)
	if err != nil {
		logger.Error("load runtime settings failed", "error", err)
		os.Exit(1)
	}
	initialEpCfg := httpserver.EndpointConfig{
		Addr:              effectiveAddr,
		TLSEnabled:        initialRuntime.TLSEnabled,
		TLSCertFile:       initialRuntime.TLSCertFile,
		TLSKeyFile:        initialRuntime.TLSKeyFile,
		TLSOwnerUIDCheck:  initialRuntime.TLSOwnerUIDCheck,
		HSTSEnabled:       initialRuntime.HSTSEnabled,
		HSTSMaxAgeSeconds: initialRuntime.HSTSMaxAgeSeconds,
	}
	httpSrv, actualEp, bootErr := httpserver.NewWithEndpoint(initialEpCfg, api.Handler(), logger, tlsBootStrict)
	if bootErr != nil {
		logger.Error("http server boot failed",
			"error", bootErr.Error(),
			"addr", effectiveAddr,
			"tls_requested", initialRuntime.TLSEnabled,
			"tls_boot_strict", tlsBootStrict,
		)
		os.Exit(1)
	}
	api.SetHTTPServerManager(httpSrv)
	stockV2MCPURL, err := stockV2Svc.StartAgentMCPServer()
	if err != nil {
		logger.Error("stockv2 agent MCP loopback server boot failed", "error", err)
		os.Exit(1)
	}
	stockV2Svc.WithCodexCLIExecutor(
		stockV2CodexBinary(),
		os.Getenv("CODEX_HOME"),
		stockV2MCPURL,
	)

	// M2 split-state recovery: if the DB says TLS should be enabled but the
	// runtime had to fall back to HTTP, reconcile DB back so they agree.
	if initialRuntime.TLSEnabled && !actualEp.TLSEnabled {
		logger.Log(ctx, httpserver.LevelCritical,
			"TLS_BOOT_FALLBACK_DB_RECONCILIATION",
			slog.String("addr", effectiveAddr),
			slog.String("db_tls_cert_file", initialRuntime.TLSCertFile),
			slog.String("db_tls_key_file", initialRuntime.TLSKeyFile),
		)
		reconciled := initialRuntime
		reconciled.TLSEnabled = false
		reconciled.TLSCertFile = ""
		reconciled.TLSKeyFile = ""
		reconciled.CookieSecure = false
		reconciled.HSTSEnabled = false
		if rerr := store.UpdateRuntimeSettings(ctx, reconciled); rerr != nil {
			logger.Error("failed to reconcile DB after TLS boot fallback", "error", rerr)
		}
	}

	if err := httpSrv.Start(); err != nil {
		logger.Error("http server initial bind failed", "error", err, "addr", actualEp.Addr)
		os.Exit(1)
	}
	logger.Info("phantom lancer listening",
		"addr", httpSrv.Addr(),
		"db", cfg.DBPath,
		"logFile", cfg.LogFile,
		"endpoint", actualEp,
		"tls_boot_strict", tlsBootStrict,
	)
	stockV2Svc.StartBackground(ctx)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	restartForUpdate := false
	select {
	case sig := <-stop:
		logger.Info("phantom lancer shutdown signal received", "signal", sig.String())
	case <-restartRequested:
		restartForUpdate = true
		logger.Info("phantom lancer restart requested")
	}

	shutdownTimeout := 10 * time.Second
	if restartForUpdate {
		shutdownTimeout = 2 * time.Second
	}
	logger.Info("phantom lancer shutdown starting", "restart_for_update", restartForUpdate, "timeout", shutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		if restartForUpdate && errors.Is(err, context.DeadlineExceeded) {
			logger.Warn("graceful shutdown timed out during update restart; forcing close", "timeout", shutdownTimeout.String())
		} else {
			logger.Error("shutdown failed", "error", err)
		}
	}
	logger.Info("phantom lancer http server stopped", "restart_for_update", restartForUpdate)
	if restartForUpdate {
		if selfExecPath != "" {
			logger.Info("phantom lancer preparing self-exec for update", "path", selfExecPath)
		} else {
			logger.Info("phantom lancer exiting for update restart", "restart_mode", cfg.Updates.RestartMode, "requires_external_supervisor", cfg.Updates.RestartMode == selfupdate.RestartModeExit)
		}
		orderlyClose(logger, dockerSvc, stockV2Svc, v2raySvc, codexGatewaySvc, codexSvc, store, logHandle)
		if selfExecPath != "" {
			if err := syscall.Exec(selfExecPath, os.Args, os.Environ()); err != nil {
				logger.Error("self-exec syscall.Exec failed, falling back to plain exit", "error", err, "path", selfExecPath)
			}
		}
		os.Exit(0)
	}
}

// orderlyClose shuts down the long-lived subsystems in the exact reverse
// order the defers were registered in main() so an explicit exit path (e.g.
// for self-update) releases resources correctly before os.Exit() /
// syscall.Exec() would otherwise skip the deferred calls.
func orderlyClose(logger *slog.Logger, dockerSvc *dockercontrol.Service, stockV2Svc *stockv2svc.Service, v2raySvc *v2ray.Service, codexGatewaySvc *codexgateway.Service, codexSvc *codexclient.Service, store *storage.Store, logHandle *logging.LoggerHandle) {
	warn := func(name string, err error) {
		if err != nil && logger != nil {
			logger.Warn(name+" close returned error", "error", err)
		}
	}
	if dockerSvc != nil {
		dockerSvc.Close()
	}
	if stockV2Svc != nil {
		stockV2Svc.Close()
	}
	if v2raySvc != nil {
		v2raySvc.Close()
	}
	if codexGatewaySvc != nil {
		codexGatewaySvc.Close()
	}
	if codexSvc != nil {
		codexSvc.Close()
	}
	if store != nil {
		warn("storage", store.Close())
	}
	if logHandle != nil {
		warn("logger", logHandle.Close())
	}
}

func wantsVersion(args []string) bool {
	for _, arg := range args {
		if arg == "--version" || arg == "-version" || arg == "version" {
			return true
		}
	}
	return false
}

func stockV2CodexBinary() string {
	if value := strings.TrimSpace(os.Getenv("PL_STOCKV2_CODEX_BIN")); value != "" {
		return value
	}
	return "codex"
}
