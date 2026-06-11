package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"ap-mv/assets"
	"ap-mv/internal/config"
	"ap-mv/internal/server"
)

// main starts the application.
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, cfg, assets.Templates, assets.StaticFiles); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
