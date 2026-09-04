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

// ErrPipelineDeferred は、パイプラインの実行が別タスクの再投入によって
// 継続される（今回の呼び出しでは完了しない）ことを示します。
var ErrPipelineDeferred = errors.New("pipeline deferred")

// State は、パイプライン実行中にステップが読み書きして変化していく値です。
type State struct {
	Task        *domain.Task
	Recipe      *domain.MusicRecipe
	VideoRecipe *video.Recipe
	OutputPath  string
}

// Services は、パイプライン実行中は固定の外部依存です。ステップは参照するだけで
// 書き換えません。
type Services struct {
	Workflows         *orchestrator.Workflows
	Reader            orchestrator.ContentReader
	Writer            remoteio.Writer
	VideoRunner       ports.VideoRunner
	TaskQueue         ports.TaskQueue
	Characters        *characterkit.Characters
	HistoryRepository ports.HistoryRepository
}

// Context は、パイプライン各ステップ（Step）間で引き継がれる実行コンテキストです。
// 埋め込みによるフィールド昇格で sc.Task / sc.VideoRunner のようにフラットに参照できます。
// フィールドを追加するときは、実行中に変化する値なら State、固定依存なら Services の
// どちらに属するかを必ず決めてください。
type Context struct {
	State
	Services
}

// Step は、動画生成パイプラインの1ステップを表すインターフェースです。
type Step interface {
	Name() string
	Execute(ctx context.Context, sc *Context) error
}
