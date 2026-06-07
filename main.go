package main

import (
	"context"
	"embed"
	"log/slog"
	"net/http"
	"os"

	"ap-mv/internal/adapters"
	"ap-mv/internal/builder"
	"ap-mv/internal/config"
	"ap-mv/internal/server"
)

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

	srv := server.NewHTTPServer(cfg, router)

	slog.Info("HTTP server started", "port", cfg.Port, "url", "http://localhost:"+cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func buildHandler(ctx context.Context, cfg *config.Config) (http.Handler, func(), error) {
	if err := cfg.ValidateEssentialConfig(); err != nil {
		return nil, func() {}, err
	}
	videoRunner, err := adapters.NewVertexVeoRunner(ctx, cfg)
	if err != nil {
		return nil, func() {}, err
	}

	container, err := builder.BuildContainer(ctx, cfg, videoRunner)
	if err != nil {
		return nil, func() {}, err
	}
	handlers, err := builder.BuildHandlers(assetsFS, container)
	if err != nil {
		container.Close()
		return nil, func() {}, err
	}
	handler := server.NewRouter(server.RouterHandlers{
		Auth:   handlers.Auth,
		Web:    handlers.Web,
		Worker: handlers.Worker,
	})
	return handler, container.Close, nil
}
