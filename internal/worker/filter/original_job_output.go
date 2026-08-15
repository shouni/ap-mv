package filter

import (
	"context"
	"fmt"
)

// OriginalJobOutputFilter は、以降のステップの出力先を **元ジョブ** のルートへ向け直します。
//
// 再生成系のタスクは Cloud Tasks のタスク名を衝突させないために毎回新しい JobID を採番し、
// fc.OutputPath はその新しいジョブのパスになります。作り直した成果物を独立した別作品として
// 残す操作（regenerate_cut_video / short_video_from_section）ではそれが正しい挙動です。
//
// 一方 section_video と finalize_video は、1 本の MV を少しずつ完成させる操作です。生成物が
// タスクごとに別ディレクトリへ散ると、完成した MV の実体が「メタデータは元ジョブ、動画は
// 実行のたびに別ジョブ」という状態になり、元ジョブを消しただけでは片付かないゴミが残ります。
// そこで、これらのコマンドでは最初にここで出力先を元ジョブへ戻し、キーフレーム・動画・
// Veo 使用量・メタデータのすべてを同じ場所に集めます。
type OriginalJobOutputFilter struct{}

// Name returns the receiver name.
func (OriginalJobOutputFilter) Name() string { return "original_job_output" }

// Execute repoints fc.OutputPath at the job the task's recipe came from.
func (OriginalJobOutputFilter) Execute(_ context.Context, fc *Context) error {
	if fc == nil || fc.Task == nil {
		return fmt.Errorf("original_job_output requires task")
	}
	outputPath := originalJobOutputPath(fc.Task.RecipeURL)
	if outputPath == "" {
		return fmt.Errorf("original_job_output: cannot resolve original job output path from recipe_url %q", fc.Task.RecipeURL)
	}
	fc.OutputPath = outputPath
	return nil
}
