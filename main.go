package main

import (
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed assets/templates/*
var assetsFS embed.FS

type PageData struct {
	Title string
}

func main() {
	// 構造化ロガーの設定
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	slog.Info("AP MV テンプレート疎通テストサーバーを起動しています...")

	// embed.FS から index.html テンプレートを事前パース
	tmpl, err := template.ParseFS(assetsFS, "assets/templates/index.html")
	if err != nil {
		slog.Error("テンプレートの解析に失敗しました", "error", err)
		os.Exit(1)
	}

	r := chi.NewRouter()

	// 最小限の標準ミドルウェア
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// トップ画面 (Home) の表示ハンドラー
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		data := PageData{
			Title: "Home",
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, data); err != nil {
			slog.Error("テンプレートのレンダリングに失敗しました", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	})

	// ポート設定（Cloud Run等の環境変数に準拠、デフォルトは8080）
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	slog.Info("HTTP サーバーが稼働しました", "port", port, "url", "http://localhost:"+port)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("サーバーが停止しました", "error", err)
	}
}
