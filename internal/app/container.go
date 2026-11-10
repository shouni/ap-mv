// Package app provides the DI container that builds and holds the clients from the configuration.
package app

import (
	"io"
	"log/slog"

	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/gcp-kit/tasks"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// Container holds the application's dependencies (the DI container).
type Container struct {
	Config *config.Config

	// I/O and Storage
	// Storage owns the GCS client's lifetime.
	Storage remoteio.Factory
	// Store is the window for reads, writes and signing taken from Storage.
	Store remoteio.Store

	// Foundation clients
	HTTPClient httpkit.HTTPClient

	// Asynchronous Task
	TaskEnqueuer *tasks.Enqueuer[domain.Task]
	TaskQueue    ports.TaskQueue
	// SessionStore is where login sessions are stored.
	SessionStore session.Store

	// Worker pipeline
	Pipeline ports.Pipeline

	// Data Access
	HistoryRepository ports.HistoryRepository
	JobStatus         ports.JobStatusStore

	// Closers are the resources opened during assembly; Container.Close closes them together.
	// Close does not look at individual fields so that when resources are added the builder only
	// needs to append.
	Closers []io.Closer
}

// Close safely releases every external connection resource the Container holds.
//
// It returns no error because its only caller is the single defer in server.Run, and a returned
// error would have nowhere to go but slog.Error.
func (c *Container) Close() {
	for _, closer := range c.Closers {
		if closer == nil {
			continue
		}
		if err := closer.Close(); err != nil {
			slog.Error("failed to close resource", "error", err)
		}
	}
}
