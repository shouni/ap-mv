package step

import (
	"context"
	"fmt"
	"strings"

	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"
)

// SectionSelectStep は、レシピを sc.Task.SectionIndex で指定されたセクションに属する
// カット群だけへ絞り込むパイプラインステップです。後続の VideoGenerationStep が
// セクション内カットのみを動画化することで、ショート動画を生成します。
type SectionSelectStep struct{}

// Name returns the receiver name.
func (SectionSelectStep) Name() string { return "section_select" }

// Execute trims the recipe to the cuts whose StartSec falls inside the selected section.
func (SectionSelectStep) Execute(_ context.Context, sc *Context) error {
	if sc == nil || sc.Task == nil || sc.Task.SectionIndex == nil {
		return fmt.Errorf("section_select requires task with section_index")
	}
	if err := ensureVideoRecipe(sc); err != nil {
		return err
	}
	sc.VideoRecipe.Normalize()
	// 保存済みレシピに歌詞由来の dialogue が未設定の場合（旧ジョブ含む）に備えて補完する。
	applyLyricsToVideoRecipeCuts(sc.VideoRecipe)

	sections := sc.VideoRecipe.MusicRecipe.Sections
	sectionIndex := *sc.Task.SectionIndex
	if sectionIndex < 0 || sectionIndex >= len(sections) {
		return fmt.Errorf("section_index %d is out of range (recipe has %d sections)", sectionIndex, len(sections))
	}
	// cut.SectionIndex は1始まりなので、0始まりの sectionIndex と比較する際は +1 する。
	wantSectionIndex := sectionIndex + 1

	// 保存済みメタデータの keyframe_reference は元ジョブ相対パスの場合があるため、
	// 新ジョブの出力パスで動画化する前に元ジョブのルートで絶対URI化する。
	originalBase := originalJobOutputPath(sc.Task.RecipeURL)

	cuts := make([]video.Cut, 0, len(sc.VideoRecipe.Cuts))
	for _, cut := range sc.VideoRecipe.Cuts {
		if cut.SectionIndex != wantSectionIndex {
			continue
		}
		// 元ジョブで生成済みのカットも、ショート動画はタスク指定のモデル・アスペクト比で
		// 生成し直すため、生成状態を初期化する。IsChainStart は元ジョブの生成時（または
		// scene_split の事前計画時）に付いたチェーン構造の名残なので、ここで消去して
		// 後続の VideoGenerationStep に従来どおり累積尺ベースでチェーンを組み直させる。
		cut.ResetGeneration(true)
		cut.KeyframeReference = resolveRecipeObjectURI(originalBase, cut.KeyframeReference)
		cuts = append(cuts, cut)
	}
	if len(cuts) == 0 {
		return fmt.Errorf("no cuts found in section %d (%s)", sectionIndex, sections[sectionIndex].Name)
	}
	// Veo は image_to_video ならカット尺 4/6/8 秒、reference_to_video（referenceImages）なら
	// 8 秒しか受け付けないため、セクション尺のまま保存された長いカット（キーフレームのみ
	// 生成したレシピ等）は 8 秒以下のサブカットへ分割し、各尺をサポート値に丸めてから動画
	// 生成へ渡す。さらに YouTube ショートの上限（60秒）に収まるよう、超過分のカットは切り詰める。
	// video_extension 用の 7 秒固定への正規化は、後続の VideoGenerationStep が
	// UsePreviousVideo を見て最終的に行うため、ここでは image_to_video/reference_to_video 用の
	// 尺で分割・丸めるだけでよい。
	sc.VideoRecipe.Cuts = veo.CapCutsTotalDuration(veo.ExpandCutsToSupportedDurations(cuts, false, sc.Characters, referenceImagesSupported(sc.VideoRunner)), youtubeShortMaxDurationSec)

	recipe, err := toDomainRecipe(sc.VideoRecipe)
	if err != nil {
		return err
	}
	sc.Recipe = recipe
	return nil
}

// youtubeShortMaxDurationSec は YouTube ショート動画の最大尺（秒）です。
const youtubeShortMaxDurationSec = 60.0

// resolveRecipeObjectURI は元ジョブ相対のオブジェクト参照を絶対URIへ解決します。
// すでにスキーム付きの参照、または base が導出できない場合はそのまま返します。
// base の末尾スラッシュ有無に依存せず安全に連結します。
func resolveRecipeObjectURI(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.Contains(ref, "://") || base == "" {
		return ref
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(ref, "/")
}
