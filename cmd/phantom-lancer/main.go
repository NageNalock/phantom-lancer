package main

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"phantom-lancer/internal/codex"
	"phantom-lancer/internal/config"
	"phantom-lancer/internal/events"
	"phantom-lancer/internal/httpapi"
	"phantom-lancer/internal/images"
	"phantom-lancer/internal/logging"
	logcenter "phantom-lancer/internal/logs"
	"phantom-lancer/internal/storage"
	"phantom-lancer/internal/v2ray"
	"phantom-lancer/web"
)

func main() {
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
		CodexBinary:  cfg.CodexBinary,
		CodexHome:    cfg.CodexHome,
		CookieSecure: cfg.CookieSecure,
	}); err != nil {
		logger.Error("initialize runtime settings failed", "error", err)
		os.Exit(1)
	}
	runtimeSettings, err := store.GetRuntimeSettings(ctx)
	if err != nil {
		logger.Error("load runtime settings failed", "error", err)
		os.Exit(1)
	}

	staticFS, err := fs.Sub(web.Files, "dist")
	if err != nil {
		logger.Error("load embedded web assets failed", "error", err)
		os.Exit(1)
	}

	hub := events.NewHub()
	codexSvc := codex.NewService(runtimeSettings.CodexBinary, runtimeSettings.CodexHome, store, hub)
	defer codexSvc.Close()
	v2raySvc := v2ray.NewService(store, hub, cfg.DataDir, logger)
	defer v2raySvc.Close()
	imagesSvc := images.NewService(store, hub, cfg.DataDir, logger)
	logsSvc := logcenter.NewService(store, cfg)
	if err := imagesSvc.Ensure(ctx); err != nil {
		logger.Error("initialize images settings failed", "error", err)
		os.Exit(1)
	}
	if err := v2raySvc.Ensure(ctx); err != nil {
		logger.Error("initialize v2ray settings failed", "error", err)
		os.Exit(1)
	}
	if v2raySettings, err := store.GetV2RaySettings(ctx); err == nil && v2raySettings.Enabled && v2raySettings.StartOnPhantomLaunch {
		if _, err := v2raySvc.Start(ctx); err != nil {
			logger.Error("start embedded v2ray failed", "error", err)
		}
	}
	api, err := httpapi.New(cfg, store, hub, codexSvc, v2raySvc, imagesSvc, logsSvc, staticFS, logger)
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
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown failed", "error", err)
	}
}
