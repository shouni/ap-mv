package step

import (
	"context"
	"fmt"

	"github.com/shouni/ap-mv/internal/domain"
)

// RecipeSaveStep は、台本生成とカット割りを終えた VideoRecipe を保存し、キーフレーム
// 生成の手前でパイプラインを終わらせるステップです。以前の「下書き」がこれにあたります。
//
// 保存先は完成ジョブと同じ {OutputPath}/video_music_meta.json です。下書き専用の場所と
// ファイル名を分けていたのをやめたのは、中身が同じ VideoRecipe で、一覧の走査規則も
// 同じだったためです。分けている限り一覧・取得・削除を 2 系統維持することになり、
// 「まだ焼いていないジョブ」を履歴として扱えませんでした。段階は JobProgress が
// keyframe / 動画のカット数から導くので、保存場所で区別する必要がありません。
//
// 保存するのは SceneSplitStep を通した後のレシピです。台本直後のカット列は尺が
// 確定しておらず（SceneSplit が達成可能なチェーン長へ割り付け、丸め誤差を次カットへ
// 送り、StartSec/EndSec を連結後の映像タイムラインへ振り直す）、それを見せても
// 実際に焼かれるカット割りとは別物になります。SceneSplitStep は同じレシピを
// 二度通しても結果が変わらないため（TestSceneSplitStepIsIdempotent）、この保存を
// mv_from_keyframe_video_recipe へそのまま渡してもカット割りは保たれます。
type RecipeSaveStep struct{}

// Name returns the receiver name.
func (RecipeSaveStep) Name() string { return "recipe_save" }

// Execute validates the recipe and writes it as the job's metadata.
func (RecipeSaveStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil || sc.VideoRecipe == nil {
		return fmt.Errorf("recipe_save requires a video recipe")
	}
	// 保存前に検証しておく。ここを通さないと、一覧には載るがキーフレーム生成で落ちる
	// レシピが残り、失敗の原因が台本作成時まで遡らないと分からなくなる。
	if err := domain.ValidateVideoRecipe(sc.VideoRecipe); err != nil {
		return fmt.Errorf("recipe_save: %w", err)
	}
	return PublishingStep{}.Execute(ctx, sc)
}
