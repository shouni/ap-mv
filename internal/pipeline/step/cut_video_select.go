package step

import (
	"context"
	"fmt"

	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"
)

// CutVideoSelectStep is the pipeline step that returns only the given cut (and the rest of its
// continuation chain) of a stored recipe to video generation. The other cuts stay
// status=generated, so the later VideoGenerationStep skips them and ChainFinalizeStep re-joins
// them with the existing videos into one.
//
// Unlike SectionSelectStep it does not narrow the cuts (every cut stays in the recipe), because
// the finished video is the join of every cut. Narrowing would turn it into a short of just that part.
//
// UsePreviousVideo must match VideoGenerationStep. In the continuation-chain approach, remaking
// one cut's video leaves stale the input of the later cuts that referenced it as
// PreviousVideoURI. So not only the target cut but the rest of the same chain is remade too.
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
	// Fill in lyric-derived dialogue in case the stored recipe lacks it (old jobs included).
	applyLyricsToVideoRecipeCuts(sc.VideoRecipe)

	cuts := sc.VideoRecipe.Cuts
	target := indexOfCutIndex(cuts, *sc.Task.CutIndex)
	if target < 0 {
		return fmt.Errorf("cut_index %d not found in recipe (%d cuts)", *sc.Task.CutIndex, len(cuts))
	}

	// The stored metadata's keyframe_reference may be relative to the original job, so make it an
	// absolute URI from the original job's root before turning it into video under the new job's
	// output path (the cuts being remade use the original job's images as-is).
	originalBase := originalJobOutputPath(sc.Task.RecipeURL)
	for i := range cuts {
		cuts[i].KeyframeReference = resolveRecipeObjectURI(originalBase, cuts[i].KeyframeReference)
	}

	end := veo.ChainTailEnd(cuts, target, f.UsePreviousVideo)
	for i := target; i <= end; i++ {
		// Keep the keyframe (only the video is remade). IsChainStart is rebuilt from durations by
		// VideoGenerationStep, so losing it here does no harm (the same reason as SectionSelectStep).
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
