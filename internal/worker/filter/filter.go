package filter

import (
	"context"
	"errors"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-remote-io/remoteio"
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
	Writer      remoteio.OutputWriter
	VideoRunner ports.VideoRunner
	TaskQueue   ports.TaskQueue
	Characters  *characterkit.Characters
	OutputPath  string
}

type Filter interface {
	Name() string
	Execute(ctx context.Context, fc *Context) error
}
