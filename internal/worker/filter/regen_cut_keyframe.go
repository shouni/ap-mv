package filter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
)

// RegenerateCutKeyframeFilter は、指定された単一カットのキーフレームのみを再生成するパイプラインステップです。
type RegenerateCutKeyframeFilter struct{}

// Name returns the receiver name.
func (RegenerateCutKeyframeFilter) Name() string { return "regen_cut_keyframe" }

// Execute regenerates the keyframe for a single cut identified by fc.Task.CutIndex.
func (RegenerateCutKeyframeFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil || fc.Task == nil || fc.Task.CutIndex == nil {
		return fmt.Errorf("regen_cut_keyframe requires task with cut_index")
	}
	if fc.Workflows == nil || fc.Workflows.CutKeyframe == nil {
		return fmt.Errorf("regen_cut_keyframe requires CutKeyframe workflow")
	}
	if err := ensureVideoRecipe(fc); err != nil {
		return err
	}
	applyTaskCharacterIDToVideoRecipe(fc.Task, fc.VideoRecipe)

	cutIndex := *fc.Task.CutIndex
	targetIdx, err := findCutByIndex(fc.VideoRecipe.Cuts, cutIndex)
	if err != nil {
		return err
	}

	editPrompt := strings.TrimSpace(fc.Task.EditPrompt)
	targetCut := fc.VideoRecipe.Cuts[targetIdx]
	if editPrompt == "" {
		targetCut.KeyframeReference = ""
		if anchor := strings.TrimSpace(fc.Task.VisualAnchorOverride); anchor != "" {
			targetCut.VisualAnchor = anchor
		}
	}
	tempRecipe := &orchestrator.VideoRecipe{
		ProjectTitle: fc.VideoRecipe.ProjectTitle,
		MusicRecipe:  fc.VideoRecipe.MusicRecipe,
		Cuts:         []orchestrator.Cut{targetCut},
	}

	regenOutputPath := fmt.Sprintf("%sregens/cut-%d/", fc.OutputPath, cutIndex)
	var updatedTemp *orchestrator.VideoRecipe
	if editPrompt != "" {
		// 編集モード: 既存キーフレームを構図はそのまま保ちつつ editPrompt の指示だけ反映する。
		updatedTemp, err = fc.Workflows.CutKeyframe.EditAndSave(ctx, tempRecipe, editPrompt, regenOutputPath)
		if errors.Is(err, orchestrator.ErrEditingNotSupported) {
			// 設定済みの画像生成エンジンが編集（EditCut）に対応していない場合、
			// editPrompt を VisualAnchor に追記した上で全体再生成にフォールバックする。
			// 構図・ポーズの保持は失われるが、編集指示自体は反映を試みる。
			tempRecipe.Cuts[0].KeyframeReference = ""
			tempRecipe.Cuts[0].VisualAnchor = strings.TrimSpace(tempRecipe.Cuts[0].VisualAnchor + "\n" + editPrompt)
			updatedTemp, err = fc.Workflows.CutKeyframe.RunAndSave(ctx, tempRecipe, regenOutputPath)
		}
		if err != nil {
			return fmt.Errorf("edit cut %d keyframe: %w", cutIndex, err)
		}
	} else {
		updatedTemp, err = fc.Workflows.CutKeyframe.RunAndSave(ctx, tempRecipe, regenOutputPath)
		if err != nil {
			return fmt.Errorf("regenerate cut %d keyframe: %w", cutIndex, err)
		}
	}

	if fc.Task.OverwriteKeyframe {
		if updatedTemp == nil || len(updatedTemp.Cuts) == 0 {
			return fmt.Errorf("regenerated keyframe not found for cut %d", cutIndex)
		}
		// updatedTemp.Cuts[0] originated as a copy of fc.VideoRecipe.Cuts[targetIdx] (targetCut above),
		// so overwriting the whole cut also carries over the VisualAnchor override applied earlier
		// without repeating that logic here.
		fc.VideoRecipe.Cuts[targetIdx] = updatedTemp.Cuts[0]
		if fc.Workflows.Publish != nil {
			// 元ジョブの recipe を上書きする。fc.OutputPath は新規ジョブのパスのため、
			// RecipeURL のディレクトリから元のジョブルートパスを導出する。
			originalOutputPath := originalJobOutputPath(fc.Task.RecipeURL)
			if _, err := fc.Workflows.Publish.Run(ctx, fc.VideoRecipe, originalOutputPath); err != nil {
				return fmt.Errorf("save updated recipe after regen cut %d: %w", cutIndex, err)
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
