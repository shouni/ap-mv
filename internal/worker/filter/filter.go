package filter

import (
	"context"
	"errors"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"ap-mv/internal/domain"
	"ap-mv/internal/ports"
)

var ErrPipelineDeferred = errors.New("pipeline deferred")

type Context struct {
	Task        *domain.Task
	Recipe      *domain.MusicRecipe
	VideoRecipe *orchestrator.VideoRecipe
	Workflows   *orchestrator.Workflows
	Reader      orchestrator.ContentReader
	VideoRunner ports.VideoRunner
	TaskQueue   ports.TaskQueue
	OutputPath  string
}

type Filter interface {
	Name() string
	Execute(ctx context.Context, fc *Context) error
}
