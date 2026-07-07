package filter

import (
	"context"
	"errors"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-remote-io/remoteio"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// ErrPipelineDeferred は、パイプラインの実行が別タスクの再投入によって
// 継続される（今回の呼び出しでは完了しない）ことを示します。
var ErrPipelineDeferred = errors.New("pipeline deferred")

// Context は、パイプライン各ステップ（Filter）間で引き継がれる実行状態です。
type Context struct {
	Task              *domain.Task
	Recipe            *domain.MusicRecipe
	VideoRecipe       *orchestrator.VideoRecipe
	Workflows         *orchestrator.Workflows
	Reader            orchestrator.ContentReader
	Writer            remoteio.OutputWriter
	VideoRunner       ports.VideoRunner
	TaskQueue         ports.TaskQueue
	Characters        *characterkit.Characters
	OutputPath        string
	HistoryRepository ports.HistoryRepository
}

// Filter は、動画生成パイプラインの1ステップを表すインターフェースです。
type Filter interface {
	Name() string
	Execute(ctx context.Context, fc *Context) error
}
