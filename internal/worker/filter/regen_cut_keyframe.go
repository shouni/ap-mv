package filter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

// RegenerateCutKeyframeFilter は、既存ジョブのキーフレームを再生成するパイプラインステップです。
// 対象は Task.CutIndex（単一カット）または Task.SectionIndex（そのセクションに属する全カット）で、
// どちらであるかは resolveRegenTargets が1箇所で解決します。
type RegenerateCutKeyframeFilter struct{}

// Name returns the receiver name.
func (RegenerateCutKeyframeFilter) Name() string { return "regen_cut_keyframe" }

// Execute regenerates the keyframes for the cuts this task targets.
func (RegenerateCutKeyframeFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil || fc.Task == nil {
		return fmt.Errorf("regen_cut_keyframe requires task")
	}
	if fc.Workflows == nil || fc.Workflows.CutKeyframe == nil {
		return fmt.Errorf("regen_cut_keyframe requires CutKeyframe workflow")
	}
	if err := ensureVideoRecipe(fc); err != nil {
		return err
	}
	applyTaskCharacterIDToVideoRecipe(fc.Task, fc.VideoRecipe)

	targets, label, err := resolveRegenTargets(fc)
	if err != nil {
		return err
	}
	basePath := fmt.Sprintf("%sregens/%s/", fc.OutputPath, label)

	var regenerated []video.Cut
	if editPrompt := strings.TrimSpace(fc.Task.EditPrompt); editPrompt != "" {
		regenerated, err = editTargetKeyframes(ctx, fc, targets, editPrompt, basePath)
	} else {
		regenerated, err = regenerateTargetKeyframes(ctx, fc, targets, basePath)
	}
	if err != nil {
		return err
	}

	if fc.Task.OverwriteKeyframe {
		if len(regenerated) != len(targets) {
			return fmt.Errorf("regenerated %d keyframes for %d target cuts", len(regenerated), len(targets))
		}
		// regenerated の各要素は対応する fc.VideoRecipe.Cuts[idx] のコピーを起点にしているため、
		// カットごと差し替えるだけで VisualAnchor の差し替えも一緒に反映される。
		for i, idx := range targets {
			fc.VideoRecipe.Cuts[idx] = regenerated[i]
		}
		if fc.Workflows.Publish != nil {
			// 元ジョブの recipe を上書きする。fc.OutputPath は新規ジョブのパスのため、
			// RecipeURL のディレクトリから元のジョブルートパスを導出する。
			originalOutputPath := originalJobOutputPath(fc.Task.RecipeURL)
			if _, err := fc.Workflows.Publish.Run(ctx, fc.VideoRecipe, originalOutputPath); err != nil {
				return fmt.Errorf("save updated recipe after regenerating %s: %w", label, err)
			}
			// 元ジョブの metadata を上書きしたため、History Detail が古いキャッシュを
			// 返し続けないよう、同一プロセス内のキャッシュを無効化する。
			if fc.HistoryRepository != nil && fc.Task.OriginalJobID != "" {
				fc.HistoryRepository.InvalidateJob(fc.Task.OriginalJobID)
			}
		}
	}

	recipe, err := toDomainRecipe(fc.VideoRecipe)
	if err != nil {
		return err
	}
	fc.Recipe = recipe
	return nil
}

// resolveRegenTargets は再生成対象カットの添字列と、出力先ディレクトリ名に使うラベルを返します。
// cut_index（単一カット）と section_index（セクション内の全カット）のどちらか一方が必要で、
// 両方指定された場合は cut_index を優先します。
func resolveRegenTargets(fc *Context) (targets []int, label string, err error) {
	if fc.Task.CutIndex != nil {
		idx, err := findCutByIndex(fc.VideoRecipe.Cuts, *fc.Task.CutIndex)
		if err != nil {
			return nil, "", err
		}
		return []int{idx}, fmt.Sprintf("cut-%d", *fc.Task.CutIndex), nil
	}
	if fc.Task.SectionIndex == nil {
		return nil, "", fmt.Errorf("regen_cut_keyframe requires task with cut_index or section_index")
	}
	// 保存済みレシピの cut.SectionIndex が未設定（旧ジョブ）でも所属を判定できるよう、
	// StartSec から補完させてから絞り込む。
	fc.VideoRecipe.Normalize()

	sectionIndex := *fc.Task.SectionIndex
	sections := fc.VideoRecipe.MusicRecipe.Sections
	if sectionIndex < 0 || sectionIndex >= len(sections) {
		return nil, "", fmt.Errorf("section_index %d is out of range (recipe has %d sections)", sectionIndex, len(sections))
	}
	// cut.SectionIndex は1始まりなので、0始まりの sectionIndex と比較する際は +1 する
	// （SectionSelectFilter と同じ規則）。
	wantSectionIndex := sectionIndex + 1
	for i, cut := range fc.VideoRecipe.Cuts {
		if cut.SectionIndex == wantSectionIndex {
			targets = append(targets, i)
		}
	}
	if len(targets) == 0 {
		return nil, "", fmt.Errorf("no cuts found in section %d (%s)", sectionIndex, sections[sectionIndex].Name)
	}
	return targets, fmt.Sprintf("section-%d", sectionIndex), nil
}

// regenerateTargetKeyframes は対象カットをプロンプトから作り直します（フル再生成）。
// RunAndSave は元々複数カットを並列生成するため、セクション対象でも呼び出しは1回で済み、
// セクション内のカットが同時に焼き直されることで scene beat の役割分担も揃います。
func regenerateTargetKeyframes(ctx context.Context, fc *Context, targets []int, basePath string) ([]video.Cut, error) {
	cuts := make([]video.Cut, 0, len(targets))
	for _, idx := range targets {
		cut := fc.VideoRecipe.Cuts[idx]
		cut.KeyframeReference = ""
		// ビジュアルアンカーの差し替えは単一カット指定のときだけ意味を持つ（セクション対象へ
		// 適用すると全カットが同じ文言になり、カットごとの絵の作り分けが失われる）。
		if len(targets) == 1 {
			if anchor := strings.TrimSpace(fc.Task.VisualAnchorOverride); anchor != "" {
				cut.VisualAnchor = anchor
			}
		}
		cuts = append(cuts, cut)
	}
	updated, err := fc.Workflows.CutKeyframe.GenerateAndSave(ctx, newRegenTempRecipe(fc, cuts), basePath)
	if err != nil {
		return nil, fmt.Errorf("regenerate keyframes: %w", err)
	}
	if updated == nil {
		return nil, fmt.Errorf("regenerated keyframes not found")
	}
	return updated.Cuts, nil
}

// editTargetKeyframes は対象カットを1枚ずつ編集モードで焼き直します。EditAndSave は既存画像を
// 編集ソースにする都合上、単一カットのレシピしか受け付けないため、セクション対象でもカットごとに
// 呼び出します（同じ指示を各カットへ順に適用する）。
func editTargetKeyframes(ctx context.Context, fc *Context, targets []int, editPrompt, basePath string) ([]video.Cut, error) {
	edited := make([]video.Cut, 0, len(targets))
	for _, idx := range targets {
		cut := fc.VideoRecipe.Cuts[idx]
		tempRecipe := newRegenTempRecipe(fc, []video.Cut{cut})
		outputPath := regenCutOutputPath(basePath, cut.CutIndex, len(targets))

		updated, err := fc.Workflows.CutKeyframe.EditAndSave(ctx, tempRecipe, 0, editPrompt, outputPath)
		if errors.Is(err, orchestrator.ErrEditingNotSupported) {
			// 設定済みの画像生成エンジンが編集（EditCut）に対応していない場合、
			// editPrompt を VisualAnchor に追記した上で全体再生成にフォールバックする。
			// 構図・ポーズの保持は失われるが、編集指示自体は反映を試みる。
			tempRecipe.Cuts[0].KeyframeReference = ""
			tempRecipe.Cuts[0].VisualAnchor = strings.TrimSpace(tempRecipe.Cuts[0].VisualAnchor + "\n" + editPrompt)
			updated, err = fc.Workflows.CutKeyframe.GenerateAndSave(ctx, tempRecipe, outputPath)
		}
		if err != nil {
			return nil, fmt.Errorf("edit cut %d keyframe: %w", cut.CutIndex, err)
		}
		if updated == nil || len(updated.Cuts) == 0 {
			return nil, fmt.Errorf("regenerated keyframe not found for cut %d", cut.CutIndex)
		}
		edited = append(edited, updated.Cuts[0])
	}
	return edited, nil
}

// newRegenTempRecipe は再生成対象カットだけを載せた一時レシピを組み立てます。MusicRecipe を
// 引き継ぐのは、キーフレームのプロンプト構築が曲側の情報（セクション・歌詞）を参照するためです。
func newRegenTempRecipe(fc *Context, cuts []video.Cut) *video.Recipe {
	return &video.Recipe{
		ProjectTitle: fc.VideoRecipe.ProjectTitle,
		MusicRecipe:  fc.VideoRecipe.MusicRecipe,
		Cuts:         cuts,
	}
}

// regenCutOutputPath は編集モードでカット1枚を書き出す先を返します。単一カット対象のときは
// basePath をそのまま使い（従来の regens/cut-<n>/ を維持）、セクション対象のときだけカットごとの
// サブディレクトリへ分けて、各呼び出しが書く keyframe_1.png の名前衝突を避けます。
func regenCutOutputPath(basePath string, cutIndex, targetCount int) string {
	if targetCount == 1 {
		return basePath
	}
	return fmt.Sprintf("%scut-%d/", basePath, cutIndex)
}
