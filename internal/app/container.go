package app

import (
	"log/slog"

	"github.com/shouni/gcp-kit/tasks"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"

	"ap-mv/internal/config"
	"ap-mv/internal/domain"
	"ap-mv/internal/ports"
)

// Container はアプリケーションの依存関係（DIコンテナ）を保持します。
type Container struct {
	Config *config.Config

	// I/O and Storage
	RemoteIO *RemoteIO

	// Foundation clients
	HTTPClient httpkit.HTTPClient

	// Asynchronous Task
	TaskEnqueuer *tasks.Enqueuer[domain.Task]
	TaskQueue    ports.TaskQueue

	// Worker pipeline
	Pipeline ports.Pipeline

	// Data Access
	HistoryRepository ports.HistoryRepository
}

// RemoteIO は外部ストレージ操作に関するコンポーネントをまとめます。
type RemoteIO struct {
	Factory remoteio.IOFactory
	Reader  remoteio.InputReader
	Writer  remoteio.OutputWriter
	Signer  remoteio.URLSigner
}

// Close は、RemoteIO が保持する Factory などの内部リソースを解放します。
func (r *RemoteIO) Close() error {
	if r.Factory != nil {
		return r.Factory.Close()
	}
	return nil
}

// Close は、Container が保持するすべての外部接続リソースを安全に解放します。
func (c *Container) Close() {
	if c.RemoteIO != nil {
		if err := c.RemoteIO.Close(); err != nil {
			slog.Error("failed to close RemoteIO", "error", err)
		}
	}
	if c.TaskEnqueuer != nil {
		if err := c.TaskEnqueuer.Close(); err != nil {
			slog.Error("failed to close task enqueuer", "error", err)
		}
	}
	if c.Pipeline != nil {
		if err := c.Pipeline.Close(); err != nil {
			slog.Error("failed to close pipeline", "error", err)
		}
	}
}
