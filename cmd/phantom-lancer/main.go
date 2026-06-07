package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"phantom-lancer/internal/buildinfo"
	"phantom-lancer/internal/codexclient"
	"phantom-lancer/internal/codexgateway"
	"phantom-lancer/internal/config"
	"phantom-lancer/internal/events"
	"phantom-lancer/internal/httpapi"
	"phantom-lancer/internal/images"
	"phantom-lancer/internal/logging"
	logcenter "phantom-lancer/internal/logs"
	"phantom-lancer/internal/selfupdate"
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

	ctx := context.Background()
	store, err := storage.Open(ctx, cfg.DBPath)
	if err != nil {
		logger.Error("open storage failed", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.EnsureRuntimeSettings(ctx, storage.RuntimeSettings{
		AllowedRoots: cfg.AllowedRoots,
		CookieSecure: cfg.CookieSecure,
	}); err != nil {
		logger.Error("initialize runtime settings failed", "error", err)
		os.Exit(1)
	}
	if err := store.PurgeLegacyCodexData(ctx); err != nil {
		logger.Error("purge legacy codex data failed", "error", err)
		os.Exit(1)
	}
	if _, err := store.GetRuntimeSettings(ctx); err != nil {
		logger.Error("load runtime settings failed", "error", err)
		os.Exit(1)
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
	v2raySvc := v2ray.NewService(store, hub, cfg.DataDir, logger)
	defer v2raySvc.Close()
	imagesSvc := images.NewService(store, hub, cfg.DataDir, logger)
	logsSvc := logcenter.NewService(store, cfg)
	restartRequested := make(chan struct{}, 1)
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
			select {
			case restartRequested <- struct{}{}:
			default:
			}
		},
	})
	updateSvc.ConfirmBoot(ctx)
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
	codexSvc.StartBackground(ctx)
	if v2raySettings, err := store.GetV2RaySettings(ctx); err == nil && v2raySettings.Enabled && v2raySettings.StartOnPhantomLaunch {
		if _, err := v2raySvc.Start(ctx); err != nil {
			logger.Error("start embedded v2ray failed", "error", err)
		}
	}
	api, err := httpapi.New(cfg, store, hub, codexGatewaySvc, codexSvc, v2raySvc, imagesSvc, logsSvc, updateSvc, staticFS, logger)
	if err != nil {
		logger.Error("create api failed", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("phantom lancer listening", "addr", cfg.Addr, "db", cfg.DBPath, "logFile", cfg.LogFile)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	restartForUpdate := false
	select {
	case <-stop:
	case <-restartRequested:
		restartForUpdate = true
		logger.Info("phantom lancer restart requested")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown failed", "error", err)
		if closeErr := server.Close(); closeErr != nil {
			logger.Error("force close server failed", "error", closeErr)
		}
	}
	if restartForUpdate {
		logger.Info("phantom lancer exiting for update restart")
		_ = logHandle.Close()
		os.Exit(0)
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
