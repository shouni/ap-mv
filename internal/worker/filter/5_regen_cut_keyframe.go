package filter

import (
	"context"
	"fmt"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
)

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

	targetCut := fc.VideoRecipe.Cuts[targetIdx]
	targetCut.KeyframeReference = ""
	tempRecipe := &orchestrator.VideoRecipe{
		ProjectTitle: fc.VideoRecipe.ProjectTitle,
		MusicRecipe:  fc.VideoRecipe.MusicRecipe,
		Cuts:         []orchestrator.Cut{targetCut},
	}

	regenOutputPath := fmt.Sprintf("%sregens/cut-%d/", fc.OutputPath, cutIndex)
	updatedTemp, err := fc.Workflows.CutKeyframe.RunAndSave(ctx, tempRecipe, regenOutputPath)
	if err != nil {
		return fmt.Errorf("regenerate cut %d keyframe: %w", cutIndex, err)
	}

	if fc.Task.OverwriteKeyframe {
		fc.VideoRecipe.Cuts[targetIdx].KeyframeReference = updatedTemp.Cuts[0].KeyframeReference
		if fc.Workflows.Publish != nil {
			if _, err := fc.Workflows.Publish.Run(ctx, fc.VideoRecipe, fc.OutputPath); err != nil {
				return fmt.Errorf("save updated recipe after regen cut %d: %w", cutIndex, err)
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
