package step

import (
	"context"
	"fmt"
	"strings"
)

// SectionKeyframeStep is the pipeline step that renders only those cuts of the target section
// that **do not have a keyframe yet**. As the stage before section_video, it is used to advance
// "keyframes → video" per section in one go from a script-only state.
//
// It differs from RegenerateCutKeyframeStep in not touching cuts already rendered. That one is the
// "remake a picture I don't like" operation and always re-renders its target; this one "fills in
// what is missing". Wanting to redo only the video generation after rendering a section is common,
// and paying for images again every time would be wasteful and silently replace pictures already approved.
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
	// Fill in lyric-derived dialogue in case the stored recipe lacks it (script-only drafts included).
	applyLyricsToVideoRecipeCuts(sc.VideoRecipe)

	// This command does not use cut_index, so resolveRegenTargets takes the section_index path, and
	// checking for an out-of-range section or no matching cut happens there in one go.
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
		// Everything is already there. Pass on to video generation without charging or saving.
		return nil
	}

	// The output goes to sections/ rather than regens/ because this is a first render, not a re-render.
	// The path itself becomes the record of "why this image came to be".
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
