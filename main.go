package main

import (
	"context"
	"embed"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"ap-mv/internal/adapters"
	"ap-mv/internal/builder"
	"ap-mv/internal/config"
	"ap-mv/internal/ports"
	"ap-mv/internal/web"
	"ap-mv/internal/worker/event"
	"ap-mv/internal/worker/pipeline"
)

const localCSRFSessionSecret = "local-development-csrf-session-secret"

//go:embed assets/templates/*
var assetsFS embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.LoadConfig()
	router, cleanup, err := buildHandler(context.Background(), cfg)
	if err != nil {
		slog.Error("application initialization failed", "error", err)
		os.Exit(1)
	}
	defer cleanup()

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      serverWriteTimeout(cfg),
		IdleTimeout:       60 * time.Second,
	}

	slog.Info("HTTP server started", "port", cfg.Port, "url", "http://localhost:"+cfg.Port, "env", appEnv())
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func buildHandler(ctx context.Context, cfg *config.Config) (http.Handler, func(), error) {
	var videoRunner ports.VideoRunner = adapters.NewMockVeoRunner(cfg)
	if isProduction() {
		if err := cfg.ValidateEssentialConfig(); err != nil {
			return nil, func() {}, err
		}
		runner, err := adapters.NewVertexVeoRunner(ctx, cfg)
		if err != nil {
			return nil, func() {}, err
		}
		videoRunner = runner
		container, err := builder.BuildContainer(ctx, cfg, videoRunner)
		if err != nil {
			return nil, func() {}, err
		}
		handler, err := web.NewRouter(assetsFS, container.TaskQueue, event.Dispatcher{Pipeline: container.Pipeline}, cfg.SessionSecret)
		if err != nil {
			container.Close()
			return nil, func() {}, err
		}
		return handler, container.Close, nil
	}

	pipe := pipeline.New(videoRunner)
	queue := ports.InlineTaskQueue{}
	dispatcher := event.Dispatcher{Pipeline: pipe}
	handler, err := web.NewRouter(assetsFS, queue, dispatcher, routerSessionSecret(cfg))
	return handler, func() {}, err
}

func routerSessionSecret(cfg *config.Config) string {
	if cfg == nil || strings.TrimSpace(cfg.SessionSecret) == "" {
		return localCSRFSessionSecret
	}
	return cfg.SessionSecret
}

func serverWriteTimeout(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.VeoOperationTimeout <= 0 {
		return 21 * time.Minute
	}
	return cfg.VeoOperationTimeout + time.Minute
}

func isProduction() bool {
	env := appEnv()
	return env == "production" || env == "prod"
}

func appEnv() string {
	env := strings.TrimSpace(os.Getenv("APP_ENV"))
	if env == "" {
		env = strings.TrimSpace(os.Getenv("ENV"))
	}
	if env == "" {
		return "local"
	}
	return strings.ToLower(env)
}
