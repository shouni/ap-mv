package step

import (
	"context"
	"errors"
	"fmt"
	"strings"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

// RegenerateCutKeyframeStep is the pipeline step that regenerates an existing job's keyframes.
// The target is Task.CutIndex (a single cut) or Task.SectionIndex (every cut in that section),
// and resolveRegenTargets decides which in one place.
type RegenerateCutKeyframeStep struct{}

// Name returns the receiver name.
func (RegenerateCutKeyframeStep) Name() string { return "regen_cut_keyframe" }

// Execute regenerates the keyframes for the cuts this task targets.
func (RegenerateCutKeyframeStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil || sc.Task == nil {
		return fmt.Errorf("regen_cut_keyframe requires task")
	}
	if sc.Workflows == nil || sc.Workflows.CutKeyframe == nil {
		return fmt.Errorf("regen_cut_keyframe requires CutKeyframe workflow")
	}
	if err := ensureVideoRecipe(sc); err != nil {
		return err
	}
	applyTaskCharacterIDToVideoRecipe(sc.Task, sc.VideoRecipe)

	targets, label, err := resolveRegenTargets(sc)
	if err != nil {
		return err
	}
	basePath := fmt.Sprintf("%sregens/%s/", sc.OutputPath, label)

	var regenerated []video.Cut
	if editPrompt := strings.TrimSpace(sc.Task.EditPrompt); editPrompt != "" {
		regenerated, err = editTargetKeyframes(ctx, sc, targets, editPrompt, basePath)
	} else {
		regenerated, err = regenerateTargetKeyframes(ctx, sc, targets, basePath)
	}
	if err != nil {
		return err
	}

	if sc.Task.OverwriteKeyframe {
		if len(regenerated) != len(targets) {
			return fmt.Errorf("regenerated %d keyframes for %d target cuts", len(regenerated), len(targets))
		}
		// Each element of regenerated starts from a copy of the corresponding sc.VideoRecipe.Cuts[idx],
		// so replacing the whole cut carries the VisualAnchor replacement along with it.
		for i, idx := range targets {
			sc.VideoRecipe.Cuts[idx] = regenerated[i]
		}
		if sc.Workflows.Publish != nil {
			// Overwrite the original job's recipe. sc.OutputPath is the new job's path, so derive the
			// original job's root path from RecipeURL's directory.
			originalOutputPath := originalJobOutputPath(sc.Task.RecipeURL)
			if _, err := sc.Workflows.Publish.Run(ctx, sc.VideoRecipe, originalOutputPath); err != nil {
				return fmt.Errorf("save updated recipe after regenerating %s: %w", label, err)
			}
		}
	}

	recipe, err := toDomainRecipe(sc.VideoRecipe)
	if err != nil {
		return err
	}
	sc.Recipe = recipe
	return nil
}

// resolveRegenTargets returns the indices of the cuts to regenerate and the label used for the
// output directory name. One of cut_index (a single cut) and section_index (every cut in the
// section) is required; when both are given, cut_index wins.
func resolveRegenTargets(sc *Context) (targets []int, label string, err error) {
	if sc.Task.CutIndex != nil {
		idx, err := findCutByIndex(sc.VideoRecipe.Cuts, *sc.Task.CutIndex)
		if err != nil {
			return nil, "", err
		}
		return []int{idx}, fmt.Sprintf("cut-%d", *sc.Task.CutIndex), nil
	}
	if sc.Task.SectionIndex == nil {
		return nil, "", fmt.Errorf("regen_cut_keyframe requires task with cut_index or section_index")
	}
	// So the section can be decided even when the stored recipe's cut.SectionIndex is unset (old
	// jobs), fill it from StartSec before filtering.
	sc.VideoRecipe.Normalize()

	sectionIndex := *sc.Task.SectionIndex
	sections := sc.VideoRecipe.MusicRecipe.Sections
	if sectionIndex < 0 || sectionIndex >= len(sections) {
		return nil, "", fmt.Errorf("section_index %d is out of range (recipe has %d sections)", sectionIndex, len(sections))
	}
	// cut.SectionIndex is 1-based, so add 1 when comparing with the 0-based sectionIndex
	// (the same rule as SectionSelectStep).
	wantSectionIndex := sectionIndex + 1
	for i, cut := range sc.VideoRecipe.Cuts {
		if cut.SectionIndex == wantSectionIndex {
			targets = append(targets, i)
		}
	}
	if len(targets) == 0 {
		return nil, "", fmt.Errorf("no cuts found in section %d (%s)", sectionIndex, sections[sectionIndex].Name)
	}
	return targets, fmt.Sprintf("section-%d", sectionIndex), nil
}

// regenerateTargetKeyframes remakes the target cuts from the prompt (full regeneration).
// RunAndSave already generates several cuts in parallel, so even for a section one call is
// enough, and re-rendering the section's cuts together keeps their scene-beat roles consistent.
func regenerateTargetKeyframes(ctx context.Context, sc *Context, targets []int, basePath string) ([]video.Cut, error) {
	cuts := make([]video.Cut, 0, len(targets))
	for _, idx := range targets {
		cut := sc.VideoRecipe.Cuts[idx]
		cut.KeyframeReference = ""
		// Replacing the visual anchor only makes sense for a single cut (applied to a section, every cut
		// would get the same wording and the per-cut differentiation of pictures would be lost).
		if len(targets) == 1 {
			if anchor := strings.TrimSpace(sc.Task.VisualAnchorOverride); anchor != "" {
				cut.VisualAnchor = anchor
			}
		}
		cuts = append(cuts, cut)
	}
	updated, err := sc.Workflows.CutKeyframe.GenerateAndSave(ctx, newRegenTempRecipe(sc, cuts), basePath)
	if err != nil {
		return nil, fmt.Errorf("regenerate keyframes: %w", err)
	}
	if updated == nil {
		return nil, fmt.Errorf("regenerated keyframes not found")
	}
	return updated.Cuts, nil
}

// editTargetKeyframes re-renders the target cuts one at a time in edit mode. EditAndSave uses the
// existing image as the edit source and so accepts only a single-cut recipe; for a section it is
// called per cut (applying the same instruction to each cut in turn).
func editTargetKeyframes(ctx context.Context, sc *Context, targets []int, editPrompt, basePath string) ([]video.Cut, error) {
	edited := make([]video.Cut, 0, len(targets))
	for _, idx := range targets {
		cut := sc.VideoRecipe.Cuts[idx]
		tempRecipe := newRegenTempRecipe(sc, []video.Cut{cut})
		outputPath := regenCutOutputPath(basePath, cut.CutIndex, len(targets))

		updated, err := sc.Workflows.CutKeyframe.EditAndSave(ctx, tempRecipe, 0, editPrompt, outputPath)
		if errors.Is(err, orchestrator.ErrEditingNotSupported) {
			// When the configured image engine does not support editing (EditCut), append editPrompt to
			// VisualAnchor and fall back to a full regeneration. Keeping composition and pose is lost,
			// but the edit instruction itself is still attempted.
			tempRecipe.Cuts[0].KeyframeReference = ""
			tempRecipe.Cuts[0].VisualAnchor = strings.TrimSpace(tempRecipe.Cuts[0].VisualAnchor + "\n" + editPrompt)
			updated, err = sc.Workflows.CutKeyframe.GenerateAndSave(ctx, tempRecipe, outputPath)
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

// newRegenTempRecipe builds a temporary recipe holding only the cuts to regenerate. MusicRecipe is
// carried over because building the keyframe prompt refers to the song's information (sections, lyrics).
func newRegenTempRecipe(sc *Context, cuts []video.Cut) *video.Recipe {
	return &video.Recipe{
		ProjectTitle: sc.VideoRecipe.ProjectTitle,
		MusicRecipe:  sc.VideoRecipe.MusicRecipe,
		Cuts:         cuts,
	}
}

// regenCutOutputPath returns where edit mode writes one cut. For a single-cut target it uses
// basePath as-is (keeping the existing regens/cut-<n>/); only for a section target does it split
// into per-cut subdirectories, avoiding name collisions on the keyframe_1.png each call writes.
func regenCutOutputPath(basePath string, cutIndex, targetCount int) string {
	if targetCount == 1 {
		return basePath
	}
	return fmt.Sprintf("%scut-%d/", basePath, cutIndex)
}
