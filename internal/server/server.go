package server

import (
	"net/http"
	"time"

	"ap-mv/internal/config"
)

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

func writeTimeout(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.VeoOperationTimeout <= 0 {
		return 21 * time.Minute
	}
	return cfg.VeoOperationTimeout + time.Minute
}
