package filter

import (
	"context"
	"fmt"
	"strings"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
)

// SectionSelectFilter は、レシピを fc.Task.SectionIndex で指定されたセクションに属する
// カット群だけへ絞り込むパイプラインステップです。後続の VideoGenerationFilter が
// セクション内カットのみを動画化することで、ショート動画を生成します。
type SectionSelectFilter struct{}

// Name returns the receiver name.
func (SectionSelectFilter) Name() string { return "section_select" }

// Execute trims the recipe to the cuts whose StartSec falls inside the selected section.
func (SectionSelectFilter) Execute(_ context.Context, fc *Context) error {
	if fc == nil || fc.Task == nil || fc.Task.SectionIndex == nil {
		return fmt.Errorf("section_select requires task with section_index")
	}
	if err := ensureVideoRecipe(fc); err != nil {
		return err
	}
	fc.VideoRecipe.Normalize()
	// 保存済みレシピに歌詞由来の dialogue が未設定の場合（旧ジョブ含む）に備えて補完する。
	applyLyricsToVideoRecipeCuts(fc.VideoRecipe)

	sections := fc.VideoRecipe.MusicRecipe.Sections
	sectionIndex := *fc.Task.SectionIndex
	if sectionIndex < 0 || sectionIndex >= len(sections) {
		return fmt.Errorf("section_index %d is out of range (recipe has %d sections)", sectionIndex, len(sections))
	}
	start, end := sectionTimeRange(sections, sectionIndex)

	// 保存済みメタデータの keyframe_reference は元ジョブ相対パスの場合があるため、
	// 新ジョブの出力パスで動画化する前に元ジョブのルートで絶対URI化する。
	originalBase := originalJobOutputPath(fc.Task.RecipeURL)

	cuts := make([]orchestrator.Cut, 0, len(fc.VideoRecipe.Cuts))
	for _, cut := range fc.VideoRecipe.Cuts {
		if cut.StartSec < start || cut.StartSec >= end {
			continue
		}
		// 元ジョブで生成済みのカットも、ショート動画はタスク指定のモデル・アスペクト比で
		// 生成し直すため、生成状態を初期化する。
		cut.Status = orchestrator.CutStatusPending
		cut.VideoID = ""
		cut.VideoURL = ""
		cut.KeyframeReference = resolveRecipeObjectURI(originalBase, cut.KeyframeReference)
		cuts = append(cuts, cut)
	}
	if len(cuts) == 0 {
		return fmt.Errorf("no cuts found in section %d (%s)", sectionIndex, sections[sectionIndex].Name)
	}
	fc.VideoRecipe.Cuts = cuts

	recipe, err := toDomainRecipe(fc.VideoRecipe)
	if err != nil {
		return err
	}
	fc.Recipe = recipe
	return nil
}

// sectionTimeRange はセクションの正規化済み時間範囲 [start, end) を返します。
// EndSeconds が未設定のセクションは Duration から補完します
// （domain.ApplyLyricsToVideoRecipeCuts のカット割り当てと同じ規則）。
func sectionTimeRange(sections []orchestrator.Section, index int) (float64, float64) {
	sec := sections[index]
	start := float64(sec.StartSeconds)
	end := float64(sec.EndSeconds)
	if end <= start && sec.Duration > 0 {
		end = start + float64(sec.Duration)
	}
	return start, end
}

// resolveRecipeObjectURI は元ジョブ相対のオブジェクト参照を絶対URIへ解決します。
// すでにスキーム付きの参照、または base が導出できない場合はそのまま返します。
func resolveRecipeObjectURI(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.Contains(ref, "://") || base == "" {
		return ref
	}
	return base + strings.TrimLeft(ref, "/")
}
