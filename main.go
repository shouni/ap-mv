// ap-mv is a web application that generates music videos from music recipes.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/shouni/gcp-kit/cloudlog"
	"github.com/shouni/go-utils/slogctx"

	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/server"
)

// logLevelEnvKey is the environment variable that sets the log level.
const logLevelEnvKey = "LOG_LEVEL"

// main starts the application.
func main() {
	// Logger setup (LOG_LEVEL support, structured logs compatible with Cloud Logging).
	// cloudlog owns the output format and slogctx attaches context attributes;
	// the app only assembles the two.
	level := slogctx.ParseLevel(os.Getenv(logLevelEnvKey))
	slog.SetDefault(slog.New(cloudlog.NewHandler(os.Stdout, level)))

	if err := run(); err != nil {
		os.Exit(1)
	}
}

// run initialises the application and starts the server. The exit code is left to main
// so that deferred cleanups are not skipped by os.Exit.
func run() error {
	cfg, err := config.LoadConfigFromEnv()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, cfg); err != nil {
		slog.Error("server failed", "error", err)
		return err
	}
	return nil
}
