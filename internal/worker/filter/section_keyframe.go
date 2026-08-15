package filter

import (
	"context"
	"fmt"
	"strings"
)

// SectionKeyframeFilter は、対象セクションのカットのうち **まだキーフレームを持たない**
// ものだけを焼くパイプラインステップです。section_video の前段として、台本だけの状態から
// 「キーフレーム → 動画」をセクション単位で一続きに進めるために使います。
//
// RegenerateCutKeyframeFilter との違いは、既に焼けているカットを触らないことです。あちらは
// 「気に入らない絵を作り直す」操作なので対象を必ず焼き直しますが、こちらは「まだ無いものを
// 埋める」操作です。セクションを焼いた後に動画生成だけやり直したくなる場面は普通にあり、
// そこで毎回画像に課金し直すのは無駄なうえ、確認済みの絵が黙って別物に変わってしまいます。
type SectionKeyframeFilter struct{}

// Name returns the receiver name.
func (SectionKeyframeFilter) Name() string { return "section_keyframe" }

// Execute generates the missing keyframes for the cuts in the targeted section.
func (SectionKeyframeFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil || fc.Task == nil || fc.Task.SectionIndex == nil {
		return fmt.Errorf("section_keyframe requires task with section_index")
	}
	if fc.Workflows == nil || fc.Workflows.CutKeyframe == nil {
		return fmt.Errorf("section_keyframe requires CutKeyframe workflow")
	}
	if err := ensureVideoRecipe(fc); err != nil {
		return err
	}
	applyTaskCharacterIDToVideoRecipe(fc.Task, fc.VideoRecipe)
	// 保存済みレシピに歌詞由来の dialogue が未設定の場合（台本だけの下書き含む）に備えて補完する。
	applyLyricsToVideoRecipeCuts(fc.VideoRecipe)

	// cut_index は使わないコマンドなので、resolveRegenTargets は section_index 経路を通り、
	// セクション範囲外・該当カット無しの検証もそこで一括して行われる。
	targets, label, err := resolveRegenTargets(fc)
	if err != nil {
		return err
	}

	missing := make([]int, 0, len(targets))
	for _, idx := range targets {
		if strings.TrimSpace(fc.VideoRecipe.Cuts[idx].KeyframeReference) == "" {
			missing = append(missing, idx)
		}
	}
	if len(missing) == 0 {
		// 既に全部揃っている。課金も保存もせずそのまま動画生成へ渡す。
		return nil
	}

	// 出力先を regens/ ではなく sections/ に分けるのは、これが焼き直しではなく初回の
	// 焼き付けだからです。パスがそのまま「なぜこの画像が生まれたか」の記録になります。
	basePath := fmt.Sprintf("%ssections/%s/", fc.OutputPath, label)
	generated, err := regenerateTargetKeyframes(ctx, fc, missing, basePath)
	if err != nil {
		return err
	}
	if len(generated) != len(missing) {
		return fmt.Errorf("generated %d keyframes for %d cuts without one", len(generated), len(missing))
	}
	for i, idx := range missing {
		fc.VideoRecipe.Cuts[idx] = generated[i]
	}

	recipe, err := toDomainRecipe(fc.VideoRecipe)
	if err != nil {
		return err
	}
	fc.Recipe = recipe
	return nil
}
