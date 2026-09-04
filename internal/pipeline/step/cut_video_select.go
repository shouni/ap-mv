package step

import (
	"context"
	"fmt"

	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"
)

// CutVideoSelectStep は、保存済みレシピのうち指定カット（とその継続チェーンの残り）だけを
// 動画生成対象へ戻すパイプラインステップです。他のカットは status=generated のまま残すため、
// 後続の VideoGenerationStep がそれらをスキップし、ChainFinalizeStep が既存の動画と
// 合わせて 1 本へ結合し直します。
//
// SectionSelectStep と違ってカットを絞り込まない（全カットをレシピに残す）のは、完成動画が
// 全カットの結合だからです。絞り込むとその部分だけのショート動画になってしまいます。
//
// UsePreviousVideo は VideoGenerationStep と一致させます。継続チェーン方式では、あるカットの
// 動画を作り直すと、それを PreviousVideoURI として参照していた後続カットの入力が古いままに
// なります。そのため対象カットだけでなく、同じチェーンの残りも作り直します。
type CutVideoSelectStep struct {
	UsePreviousVideo bool
}

// Name returns the receiver name.
func (CutVideoSelectStep) Name() string { return "cut_video_select" }

// Execute resets the target cut (and the rest of its chain) so video generation regenerates them.
func (f CutVideoSelectStep) Execute(_ context.Context, sc *Context) error {
	if sc == nil || sc.Task == nil || sc.Task.CutIndex == nil {
		return fmt.Errorf("cut_video_select requires task with cut_index")
	}
	if err := ensureVideoRecipe(sc); err != nil {
		return err
	}
	sc.VideoRecipe.Normalize()
	// 保存済みレシピに歌詞由来の dialogue が未設定の場合（旧ジョブ含む）に備えて補完する。
	applyLyricsToVideoRecipeCuts(sc.VideoRecipe)

	cuts := sc.VideoRecipe.Cuts
	target := indexOfCutIndex(cuts, *sc.Task.CutIndex)
	if target < 0 {
		return fmt.Errorf("cut_index %d not found in recipe (%d cuts)", *sc.Task.CutIndex, len(cuts))
	}

	// 保存済みメタデータの keyframe_reference は元ジョブ相対パスの場合があるため、
	// 新ジョブの出力パスで動画化する前に元ジョブのルートで絶対URI化する
	// （作り直すカットのキーフレームは元ジョブの画像をそのまま使います）。
	originalBase := originalJobOutputPath(sc.Task.RecipeURL)
	for i := range cuts {
		cuts[i].KeyframeReference = resolveRecipeObjectURI(originalBase, cuts[i].KeyframeReference)
	}

	end := veo.ChainTailEnd(cuts, target, f.UsePreviousVideo)
	for i := target; i <= end; i++ {
		// キーフレームは残す（作り直すのは動画だけ）。IsChainStart は
		// VideoGenerationStep が尺から組み直すため、ここで消えても問題ない
		// （SectionSelectStep と同じ理由）。
		cuts[i].ResetGeneration(true)
	}

	recipe, err := toDomainRecipe(sc.VideoRecipe)
	if err != nil {
		return err
	}
	sc.Recipe = recipe
	return nil
}

// indexOfCutIndex returns the slice position of the cut whose CutIndex matches, or -1.
func indexOfCutIndex(cuts []video.Cut, cutIndex int) int {
	for i := range cuts {
		if cuts[i].CutIndex == cutIndex {
			return i
		}
	}
	return -1
}
