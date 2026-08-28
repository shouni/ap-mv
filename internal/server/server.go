package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shouni/gcp-kit/cloudrun"

	"github.com/shouni/ap-mv/internal/builder"
	"github.com/shouni/ap-mv/internal/config"
)

// Run はサーバーの構築、起動、およびライフサイクル管理を行います。
func Run(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	if err := cfg.ValidateEssentialConfig(); err != nil {
		return err
	}

	appCtx, err := builder.BuildContainer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to build application context: %w", err)
	}
	defer func() {
		slog.Info("closing application context")
		appCtx.Close()
	}()

	h, err := builder.BuildHandlers(appCtx)
	if err != nil {
		return fmt.Errorf("failed to build handlers: %w", err)
	}

	slog.InfoContext(ctx, "HTTP server started", "port", cfg.Server.Port, "service_url", cfg.Server.ServiceURL)

	// 起動・シグナル待ち・正常停止は cloudrun が持ちます。WriteTimeout だけは
	// Veo の実行時間に合わせて明示します（既定では縛られません）。
	return cloudrun.Serve(ctx, cloudrun.Config{
		Port:            cfg.Server.Port,
		Handler:         NewRouter(h, cfg.GCP.ProjectID),
		ReadTimeout:     10 * time.Second,
		WriteTimeout:    writeTimeout(cfg),
		IdleTimeout:     60 * time.Second,
		ShutdownTimeout: cfg.Server.ShutdownTimeout,
	})
}

// writeTimeout returns the server write timeout.
func writeTimeout(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.AI.VeoOperationTimeout <= 0 {
		return 21 * time.Minute
	}
	return cfg.AI.VeoOperationTimeout + time.Minute
}
