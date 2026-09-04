package step

import (
	"context"
	"fmt"
	"strings"
)

// SectionKeyframeStep は、対象セクションのカットのうち **まだキーフレームを持たない**
// ものだけを焼くパイプラインステップです。section_video の前段として、台本だけの状態から
// 「キーフレーム → 動画」をセクション単位で一続きに進めるために使います。
//
// RegenerateCutKeyframeStep との違いは、既に焼けているカットを触らないことです。あちらは
// 「気に入らない絵を作り直す」操作なので対象を必ず焼き直しますが、こちらは「まだ無いものを
// 埋める」操作です。セクションを焼いた後に動画生成だけやり直したくなる場面は普通にあり、
// そこで毎回画像に課金し直すのは無駄なうえ、確認済みの絵が黙って別物に変わってしまいます。
type SectionKeyframeStep struct{}

// Name returns the receiver name.
func (SectionKeyframeStep) Name() string { return "section_keyframe" }

// Execute generates the missing keyframes for the cuts in the targeted section.
func (SectionKeyframeStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil || sc.Task == nil || sc.Task.SectionIndex == nil {
		return fmt.Errorf("section_keyframe requires task with section_index")
	}
	if sc.Workflows == nil || sc.Workflows.CutKeyframe == nil {
		return fmt.Errorf("section_keyframe requires CutKeyframe workflow")
	}
	if err := ensureVideoRecipe(sc); err != nil {
		return err
	}
	applyTaskCharacterIDToVideoRecipe(sc.Task, sc.VideoRecipe)
	// 保存済みレシピに歌詞由来の dialogue が未設定の場合（台本だけの下書き含む）に備えて補完する。
	applyLyricsToVideoRecipeCuts(sc.VideoRecipe)

	// cut_index は使わないコマンドなので、resolveRegenTargets は section_index 経路を通り、
	// セクション範囲外・該当カット無しの検証もそこで一括して行われる。
	targets, label, err := resolveRegenTargets(sc)
	if err != nil {
		return err
	}

	missing := make([]int, 0, len(targets))
	for _, idx := range targets {
		if strings.TrimSpace(sc.VideoRecipe.Cuts[idx].KeyframeReference) == "" {
			missing = append(missing, idx)
		}
	}
	if len(missing) == 0 {
		// 既に全部揃っている。課金も保存もせずそのまま動画生成へ渡す。
		return nil
	}

	// 出力先を regens/ ではなく sections/ に分けるのは、これが焼き直しではなく初回の
	// 焼き付けだからです。パスがそのまま「なぜこの画像が生まれたか」の記録になります。
	basePath := fmt.Sprintf("%ssections/%s/", sc.OutputPath, label)
	generated, err := regenerateTargetKeyframes(ctx, sc, missing, basePath)
	if err != nil {
		return err
	}
	if len(generated) != len(missing) {
		return fmt.Errorf("generated %d keyframes for %d cuts without one", len(generated), len(missing))
	}
	for i, idx := range missing {
		sc.VideoRecipe.Cuts[idx] = generated[i]
	}

	recipe, err := toDomainRecipe(sc.VideoRecipe)
	if err != nil {
		return err
	}
	sc.Recipe = recipe
	return nil
}
