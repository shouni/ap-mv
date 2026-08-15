// Package app は、設定値から各種クライアントを組み立てて保持する DI コンテナを提供します。
package app

import (
	"io"
	"log/slog"

	"github.com/shouni/gcp-kit/tasks"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
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
	JobStatus         ports.JobStatusStore
}

// RemoteIO は外部ストレージ操作に関するコンポーネントをまとめます。
//
// 実体は go-remote-io が持つ remoteio.Bundle です。同じ構造体と組み立て関数を
// 各アプリが個別に持っていたものをライブラリへ引き取ったため、ここはアプリ内での
// 呼び名を保つための別名だけになっています（rio.Reader などの参照はそのまま使えます）。
type RemoteIO = remoteio.Bundle

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
	// 履歴リポジトリは TTL キャッシュの回収ゴルーチンを抱えます。
	if closer, ok := c.HistoryRepository.(io.Closer); ok && closer != nil {
		if err := closer.Close(); err != nil {
			slog.Error("failed to close history repository", "error", err)
		}
	}
}
