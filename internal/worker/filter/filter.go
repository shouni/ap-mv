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

// State は、パイプライン実行中にフィルターが読み書きして変化していく値です。
type State struct {
	Task        *domain.Task
	Recipe      *domain.MusicRecipe
	VideoRecipe *orchestrator.VideoRecipe
	OutputPath  string
}

// Services は、パイプライン実行中は固定の外部依存です。フィルターは参照するだけで
// 書き換えません。
type Services struct {
	Workflows         *orchestrator.Workflows
	Reader            orchestrator.ContentReader
	Writer            remoteio.OutputWriter
	VideoRunner       ports.VideoRunner
	TaskQueue         ports.TaskQueue
	Characters        *characterkit.Characters
	HistoryRepository ports.HistoryRepository
}

// Context は、パイプライン各ステップ（Filter）間で引き継がれる実行コンテキストです。
// 埋め込みによるフィールド昇格で fc.Task / fc.VideoRunner のようにフラットに参照できます。
// フィールドを追加するときは、実行中に変化する値なら State、固定依存なら Services の
// どちらに属するかを必ず決めてください。
type Context struct {
	State
	Services
}

// Filter は、動画生成パイプラインの1ステップを表すインターフェースです。
type Filter interface {
	Name() string
	Execute(ctx context.Context, fc *Context) error
}
