package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"ap-mv/internal/builder"
	"ap-mv/internal/config"
)

const defaultShutdownTimeout = 30 * time.Second

// Run はサーバーの構築、起動、およびライフサイクル管理を行います。
func Run(ctx context.Context, cfg *config.Config, templates fs.FS, staticFiles fs.FS) error {
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

	h, err := builder.BuildHandlers(templates, staticFiles, appCtx)
	if err != nil {
		return fmt.Errorf("failed to build handlers: %w", err)
	}

	srv := NewHTTPServer(cfg, NewRouter(h))
	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("HTTP server started", "port", cfg.Port, "service_url", cfg.ServiceURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		slog.Info("shutdown signal received, starting graceful shutdown")
		return gracefulShutdown(srv, cfg.ShutdownTimeout)
	}
}

// NewHTTPServer はアプリケーション用の http.Server を構築します。
func NewHTTPServer(cfg *config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      writeTimeout(cfg),
		IdleTimeout:       60 * time.Second,
	}
}

func gracefulShutdown(srv *http.Server, cfgTimeout time.Duration) error {
	timeout := cfgTimeout
	if timeout == 0 {
		timeout = defaultShutdownTimeout
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed, forcing close", "error", err)
		if closeErr := srv.Close(); closeErr != nil {
			return errors.Join(err, fmt.Errorf("subsequent server close also failed: %w", closeErr))
		}
		return fmt.Errorf("graceful shutdown failed, server was forcibly closed: %w", err)
	}

	slog.Info("server stopped cleanly")
	return nil
}

func writeTimeout(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.VeoOperationTimeout <= 0 {
		return 21 * time.Minute
	}
	return cfg.VeoOperationTimeout + time.Minute
}
