// ap-mv は、音楽レシピからミュージックビデオを生成するWebアプリケーションです。
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/shouni/gcp-kit/cloudlog"
	"github.com/shouni/go-utils/slogctx"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/server"
)

// logLevelEnvKey はログ出力レベルを指定する環境変数名です。
const logLevelEnvKey = "LOG_LEVEL"

// main starts the application.
func main() {
	// ロガーの設定（LOG_LEVEL 対応・Cloud Logging 互換の構造化ログ）。
	// 出力フォーマットは cloudlog、context 属性の付与は slogctx が担い、
	// 両者の組み立てだけをアプリ側で行う。
	level := slogctx.ParseLevel(os.Getenv(logLevelEnvKey))
	base := slog.NewJSONHandler(os.Stdout, cloudlog.HandlerOptions(level))
	slog.SetDefault(slog.New(slogctx.NewHandler(base)))

	if err := run(); err != nil {
		os.Exit(1)
	}
}

// run はアプリケーションの初期化とサーバー起動を行います。defer によるクリーンアップが
// os.Exit で無視されないよう、終了コードの決定は main 側に委ねます。
func run() error {
	cfg, err := config.LoadConfigFromEnv()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, cfg, assets.Templates, assets.StaticFiles); err != nil {
		slog.Error("server failed", "error", err)
		return err
	}
	return nil
}
