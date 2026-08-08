package filter

import (
	"context"
	"fmt"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
)

// CutVideoSelectFilter は、保存済みレシピのうち指定カット（とその継続チェーンの残り）だけを
// 動画生成対象へ戻すパイプラインステップです。他のカットは status=generated のまま残すため、
// 後続の VideoGenerationFilter がそれらをスキップし、ChainFinalizeFilter が既存の動画と
// 合わせて 1 本へ結合し直します。
//
// SectionSelectFilter と違ってカットを絞り込まない（全カットをレシピに残す）のは、完成動画が
// 全カットの結合だからです。絞り込むとその部分だけのショート動画になってしまいます。
//
// UsePreviousVideo は VideoGenerationFilter と一致させます。継続チェーン方式では、あるカットの
// 動画を作り直すと、それを PreviousVideoURI として参照していた後続カットの入力が古いままに
// なります。そのため対象カットだけでなく、同じチェーンの残りも作り直します。
type CutVideoSelectFilter struct {
	UsePreviousVideo bool
}

// Name returns the receiver name.
func (CutVideoSelectFilter) Name() string { return "cut_video_select" }

// Execute resets the target cut (and the rest of its chain) so video generation regenerates them.
func (f CutVideoSelectFilter) Execute(_ context.Context, fc *Context) error {
	if fc == nil || fc.Task == nil || fc.Task.CutIndex == nil {
		return fmt.Errorf("cut_video_select requires task with cut_index")
	}
	if err := ensureVideoRecipe(fc); err != nil {
		return err
	}
	fc.VideoRecipe.Normalize()
	// 保存済みレシピに歌詞由来の dialogue が未設定の場合（旧ジョブ含む）に備えて補完する。
	applyLyricsToVideoRecipeCuts(fc.VideoRecipe)

	cuts := fc.VideoRecipe.Cuts
	target := indexOfCutIndex(cuts, *fc.Task.CutIndex)
	if target < 0 {
		return fmt.Errorf("cut_index %d not found in recipe (%d cuts)", *fc.Task.CutIndex, len(cuts))
	}

	// 保存済みメタデータの keyframe_reference は元ジョブ相対パスの場合があるため、
	// 新ジョブの出力パスで動画化する前に元ジョブのルートで絶対URI化する
	// （作り直すカットのキーフレームは元ジョブの画像をそのまま使います）。
	originalBase := originalJobOutputPath(fc.Task.RecipeURL)
	for i := range cuts {
		cuts[i].KeyframeReference = resolveRecipeObjectURI(originalBase, cuts[i].KeyframeReference)
	}

	end := orchestrator.ChainTailEnd(cuts, target, f.UsePreviousVideo)
	for i := target; i <= end; i++ {
		// キーフレームは残す（作り直すのは動画だけ）。IsChainStart は
		// VideoGenerationFilter が尺から組み直すため、ここで消えても問題ない
		// （SectionSelectFilter と同じ理由）。
		cuts[i].ResetGeneration(true)
	}

	recipe, err := toDomainRecipe(fc.VideoRecipe)
	if err != nil {
		return err
	}
	fc.Recipe = recipe
	return nil
}

// indexOfCutIndex returns the slice position of the cut whose CutIndex matches, or -1.
func indexOfCutIndex(cuts []orchestrator.Cut, cutIndex int) int {
	for i := range cuts {
		if cuts[i].CutIndex == cutIndex {
			return i
		}
	}
	return -1
}

