// Package ports は、ap-mv の各コンポーネントが依存するインターフェース（ポート）を定義します。
package ports

import (
	"context"
	"io"

	"github.com/shouni/ap-mv/internal/domain"
)

// KeyframeSink はキーフレームダウンロード時に各ファイルごとに呼ばれるコールバックです。
// name はダウンロード時のファイル名、r はファイルの内容を読み取るストリームです。
type KeyframeSink func(name string, r io.Reader) error

// HistoryRepository loads and mutates generated MV history.
type HistoryRepository interface {
	// ListHistoryPage は MV ジョブを一覧します。stage が空なら全段階、指定すれば
	// その段階のジョブだけを返します（台本のみのジョブは domain.StageScript）。
	ListHistoryPage(ctx context.Context, page int, perPage int, stage domain.JobStage) (domain.VideoHistoryPage, error)
	// GetHistory は表示用に整形した履歴を返します。**署名付き URL は空のままです。**
	// 画面は同一オリジンのパスを辿り、ハンドラーがリダイレクトの時点で 1 本だけ署名します。
	// カット 1 本ずつ前払いすると、詳細 1 画面で数十回の IAM SignBlob 往復になります。
	GetHistory(ctx context.Context, jobID string) (domain.VideoHistoryDetail, error)
	// SignHistoryURLs は GetHistory が空のままにした署名付き URL を埋めます。
	// JSON の呼び出し元（ap-mcp）はリダイレクトを辿らずに URL 自体を受け取るため、
	// この経路だけが署名を必要とします。
	SignHistoryURLs(ctx context.Context, detail *domain.VideoHistoryDetail) error
	// SignedObjectURL は、読み込み済みの履歴から取り出した gs:// URI を署名します。
	// リダイレクトハンドラー専用で、呼び出し元の入力をそのまま署名させないために、
	// 渡すのは必ず履歴に記録されていた URI に限ります。
	SignedObjectURL(ctx context.Context, uri string) (string, error)
	// GetRecipe は保存済みの VideoRecipe をそのまま返します。編集して SaveRecipe で
	// 書き戻す往復のための読み出しで、表示用に整形した GetHistory とは別経路です。
	GetRecipe(ctx context.Context, jobID string) (*domain.VideoRecipe, error)
	// SaveRecipe はジョブの VideoRecipe を上書き保存します。生成前（台本のみ）の
	// カット割りを画像コスト 0 で直せるようにするための経路です。
	SaveRecipe(ctx context.Context, jobID string, recipe *domain.VideoRecipe) error
	DeleteHistory(ctx context.Context, jobID string) error
	DownloadKeyframes(ctx context.Context, jobID string, sink KeyframeSink) error
	KeyframeZipSignedURL(ctx context.Context, jobID string) (string, error)
	// GetVeoUsage loads the job's recorded Veo generation tally. It returns (nil, nil) for jobs
	// that predate the record or never reached video generation — a missing record is a normal
	// state, not an error, and callers fall back to the recipe-derived estimate.
	GetVeoUsage(ctx context.Context, jobID string) (*domain.VeoUsage, error)
	// InvalidateJob clears cached history/recipe metadata for jobID, so a subsequent GetHistory
	// reads fresh data from storage instead of a stale cached copy (e.g. after a regenerate/edit
	// job writes new metadata back to jobID from a worker running in the same process).
	InvalidateJob(jobID string)
}
