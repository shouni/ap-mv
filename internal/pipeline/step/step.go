package step

import (
	"context"
	"errors"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-remote-io/remoteio"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// ErrPipelineDeferred means the pipeline run continues through a re-enqueued task (it does not
// finish in this call).
var ErrPipelineDeferred = errors.New("pipeline deferred")

// State holds the values steps read and write as they change during a pipeline run.
type State struct {
	Task        *domain.Task
	Recipe      *domain.MusicRecipe
	VideoRecipe *video.Recipe
	OutputPath  string
}

// Services holds the external dependencies fixed during a pipeline run. Steps only refer to them
// and never rewrite them.
type Services struct {
	Workflows         *orchestrator.Workflows
	Reader            orchestrator.ContentReader
	Writer            remoteio.Writer
	VideoRunner       ports.VideoRunner
	TaskQueue         ports.TaskQueue
	Characters        *characterkit.Characters
	HistoryRepository ports.HistoryRepository
}

// Context is the execution context handed between the pipeline's steps (Step).
// Field promotion through embedding lets it be referenced flatly, as in sc.Task / sc.VideoRunner.
// When adding a field, always decide whether it belongs to State (a value that changes during the
// run) or Services (a fixed dependency).
type Context struct {
	State
	Services
}

// Step is the interface for one step of the video-generation pipeline.
type Step interface {
	Name() string
	Execute(ctx context.Context, sc *Context) error
}
