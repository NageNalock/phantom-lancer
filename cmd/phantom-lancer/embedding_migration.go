package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"phantom-lancer/internal/config"
	stockv2svc "phantom-lancer/internal/stockv2"
	"phantom-lancer/internal/storage"
)

const stockV2EmbeddingMigrationCommand = "stockv2-embedding-migrate"

type stockV2EmbeddingMigrationOptions struct {
	configPath        string
	targetModelID     string
	targetModelName   string
	batchSize         int
	rateLimitMS       int
	maxStalledBatches int
}

func runStockV2EmbeddingMigration(args []string) int {
	opts, err := parseStockV2EmbeddingMigrationOptions(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	configArgs := make([]string, 0, 2)
	if opts.configPath != "" {
		configArgs = append(configArgs, "--config", opts.configPath)
	}
	cfg, err := config.Load(configArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load config:", err)
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	recordStockV2EmbeddingMigrationAudit(ctx, cfg.DBPath, logger, storage.AuditEvent{
		EventType: "stockv2_embedding_offline_migration_started",
		RiskLevel: "high",
		Summary:   "开始离线迁移 StockV2 向量模型",
		Payload: map[string]any{
			"targetModelId":   opts.targetModelID,
			"targetModelName": opts.targetModelName,
			"batchSize":       opts.batchSize,
			"rateLimitMs":     opts.rateLimitMS,
		},
	})

	stockStore, err := stockv2svc.NewStoreWithMarketDB(
		cfg.DBPath,
		stockv2svc.DefaultMarketDBPath(cfg.DataDir, cfg.DBPath),
	)
	if err != nil {
		logger.Error("open offline stockv2 storage failed", "error", err)
		recordStockV2EmbeddingMigrationFailure(cfg.DBPath, logger, err)
		return 1
	}
	service := stockv2svc.NewService(stockStore, logger, &http.Client{Timeout: 30 * time.Second})
	closed := false
	defer func() {
		if !closed {
			_ = service.Close()
		}
	}()

	target, err := resolveStockV2EmbeddingMigrationTarget(ctx, service, opts)
	if err != nil {
		logger.Error("resolve target embedding model failed", "error", err)
		_ = service.Close()
		closed = true
		recordStockV2EmbeddingMigrationFailure(cfg.DBPath, logger, err)
		return 1
	}
	marketPath := stockStore.MarketDBPath()
	marketSizeBefore := fileSize(marketPath)
	sqliteSizeBefore := fileSize(cfg.DBPath)
	logger.Info("offline embedding migration started",
		"target_model_id", target.ID,
		"target_model_name", target.ModelName,
		"embedding_dimensions", target.EmbeddingDimensions,
		"batch_size", opts.batchSize,
		"rate_limit_ms", opts.rateLimitMS,
		"sqlite_size_before", sqliteSizeBefore,
		"market_db_size_before", marketSizeBefore,
	)

	result, err := service.RunOfflineEmbeddingMigration(ctx, stockv2svc.OfflineEmbeddingMigrationRequest{
		TargetModelID:         target.ID,
		BatchSize:             opts.batchSize,
		MaintainRateLimitMs:   opts.rateLimitMS,
		MaxStalledBatches:     opts.maxStalledBatches,
		EnableAutoMaintenance: true,
	}, func(progress stockv2svc.EmbeddingMigrationProgress) {
		logger.Info("offline embedding migration progress",
			"stage", progress.Stage,
			"batch", progress.Batch,
			"source_count", progress.SourceCount,
			"ready_assets", progress.ReadyAssets,
			"failed_assets", progress.FailedAssets,
			"remaining_estimate", progress.RemainingEstimate,
			"batch_total", progress.BatchTotal,
			"batch_succeeded", progress.BatchSucceeded,
			"batch_failed", progress.BatchFailed,
		)
	})
	if err != nil {
		logger.Error("offline embedding migration failed", "error", err, "completed_batches", result.BatchCount)
		_ = service.Close()
		closed = true
		recordStockV2EmbeddingMigrationFailure(cfg.DBPath, logger, err)
		return 1
	}
	if err := service.Close(); err != nil {
		logger.Error("close offline stockv2 storage failed", "error", err)
		closed = true
		recordStockV2EmbeddingMigrationFailure(cfg.DBPath, logger, err)
		return 1
	}
	closed = true

	marketSizeAfter := fileSize(marketPath)
	sqliteSizeAfter := fileSize(cfg.DBPath)
	logger.Info("offline embedding migration completed",
		"source_model_id", result.SourceModelID,
		"target_model_id", result.TargetModelID,
		"target_model_name", result.TargetModelName,
		"embedding_dimensions", result.EmbeddingDimensions,
		"batch_count", result.BatchCount,
		"source_count", result.SourceCount,
		"deleted_assets", result.DeletedAssets,
		"deleted_vectors", result.DeletedVectors,
		"sqlite_size_before", sqliteSizeBefore,
		"sqlite_size_after", sqliteSizeAfter,
		"market_db_size_before", marketSizeBefore,
		"market_db_size_after", marketSizeAfter,
	)
	recordStockV2EmbeddingMigrationAudit(ctx, cfg.DBPath, logger, storage.AuditEvent{
		EventType: "stockv2_embedding_offline_migration_completed",
		RiskLevel: "high",
		Summary:   "完成 StockV2 离线向量模型迁移并清理旧向量",
		Payload: map[string]any{
			"sourceModelId":     result.SourceModelID,
			"targetModelId":     result.TargetModelID,
			"targetModelName":   result.TargetModelName,
			"dimensions":        result.EmbeddingDimensions,
			"sourceCount":       result.SourceCount,
			"batchCount":        result.BatchCount,
			"deletedAssets":     result.DeletedAssets,
			"deletedVectors":    result.DeletedVectors,
			"sqliteBytesBefore": sqliteSizeBefore,
			"sqliteBytesAfter":  sqliteSizeAfter,
			"marketBytesBefore": marketSizeBefore,
			"marketBytesAfter":  marketSizeAfter,
		},
	})
	return 0
}

func parseStockV2EmbeddingMigrationOptions(args []string) (stockV2EmbeddingMigrationOptions, error) {
	opts := stockV2EmbeddingMigrationOptions{}
	fs := flag.NewFlagSet(stockV2EmbeddingMigrationCommand, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.configPath, "config", "", "TOML config file path")
	fs.StringVar(&opts.targetModelID, "target-model-id", "", "configured target embedding model id")
	fs.StringVar(&opts.targetModelName, "target-model-name", "BAAI/bge-m3", "configured target embedding model name")
	fs.IntVar(&opts.batchSize, "batch-size", 200, "embedding sources per resumable batch")
	fs.IntVar(&opts.rateLimitMS, "rate-limit-ms", 500, "delay between embedding requests")
	fs.IntVar(&opts.maxStalledBatches, "max-stalled-batches", 3, "abort after consecutive batches with no successful item")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if strings.TrimSpace(opts.targetModelID) == "" && strings.TrimSpace(opts.targetModelName) == "" {
		return opts, errors.New("target-model-id or target-model-name is required")
	}
	if opts.batchSize < 1 || opts.batchSize > 200 {
		return opts, errors.New("batch-size must be between 1 and 200")
	}
	if opts.rateLimitMS < 0 {
		return opts, errors.New("rate-limit-ms cannot be negative")
	}
	if opts.maxStalledBatches < 1 {
		return opts, errors.New("max-stalled-batches must be positive")
	}
	return opts, nil
}

func resolveStockV2EmbeddingMigrationTarget(ctx context.Context, service *stockv2svc.Service, opts stockV2EmbeddingMigrationOptions) (stockv2svc.AgentModelProfile, error) {
	if id := strings.TrimSpace(opts.targetModelID); id != "" {
		model, err := service.GetAgentModelProfile(ctx, id)
		if err != nil {
			return stockv2svc.AgentModelProfile{}, err
		}
		if model.ModelType != stockv2svc.AgentModelTypeEmbedding {
			return stockv2svc.AgentModelProfile{}, stockv2svc.ErrEmbeddingModelInvalid
		}
		return model, nil
	}
	enabled := true
	models, err := service.ListAgentModelProfiles(ctx, stockv2svc.AgentModelProfileListFilter{Enabled: &enabled, Limit: 200})
	if err != nil {
		return stockv2svc.AgentModelProfile{}, err
	}
	name := strings.TrimSpace(opts.targetModelName)
	matches := make([]stockv2svc.AgentModelProfile, 0, 1)
	for _, model := range models {
		if model.ModelType == stockv2svc.AgentModelTypeEmbedding && model.ModelName == name {
			matches = append(matches, model)
		}
	}
	if len(matches) != 1 {
		return stockv2svc.AgentModelProfile{}, fmt.Errorf("target embedding model %q matched %d configured models", name, len(matches))
	}
	return matches[0], nil
}

func recordStockV2EmbeddingMigrationFailure(dbPath string, logger *slog.Logger, cause error) {
	recordStockV2EmbeddingMigrationAudit(context.Background(), dbPath, logger, storage.AuditEvent{
		EventType: "stockv2_embedding_offline_migration_failed",
		RiskLevel: "high",
		Summary:   "StockV2 离线向量模型迁移失败",
		Payload: map[string]any{
			"error": cause.Error(),
		},
	})
}

func recordStockV2EmbeddingMigrationAudit(ctx context.Context, dbPath string, logger *slog.Logger, event storage.AuditEvent) {
	store, err := storage.Open(ctx, dbPath, logger)
	if err != nil {
		logger.Error("open audit storage for embedding migration failed", "error", err, "event_type", event.EventType)
		return
	}
	defer store.Close()
	if _, err := store.AddAudit(ctx, event); err != nil {
		logger.Error("record embedding migration audit failed", "error", err, "event_type", event.EventType)
	}
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return info.Size()
}
