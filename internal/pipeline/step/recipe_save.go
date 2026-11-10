package step

import (
	"context"
	"fmt"

	"github.com/shouni/ap-mv/internal/domain"
)

// RecipeSaveStep saves the VideoRecipe once script generation and cut splitting are done, and
// ends the pipeline before keyframe generation. It is what used to be called the "draft".
//
// It is saved to the same {OutputPath}/video_music_meta.json as a finished job. A separate
// location and file name for drafts were dropped because the contents are the same VideoRecipe
// and the listing's scan rule was the same too. As long as they were separate, listing, fetching
// and deleting had to be maintained twice, and "jobs not rendered yet" could not be treated as
// history. JobProgress derives the stage from the keyframe / video cut counts, so the storage
// location does not need to tell them apart.
//
// What is saved is the recipe after SceneSplitStep. Right after the script the cuts' durations are
// not settled (SceneSplit allocates them to achievable chain lengths, carries rounding errors into
// the next cut and reassigns StartSec/EndSec on the joined video timeline), so showing that would
// differ from the cut list actually rendered. SceneSplitStep gives the same result when the same
// recipe passes through it twice (TestSceneSplitStepIsIdempotent), so passing this save straight
// to mv_from_keyframe_video_recipe keeps the cut list.
type RecipeSaveStep struct{}

// Name returns the receiver name.
func (RecipeSaveStep) Name() string { return "recipe_save" }

// Execute validates the recipe and writes it as the job's metadata.
func (RecipeSaveStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil || sc.VideoRecipe == nil {
		return fmt.Errorf("recipe_save requires a video recipe")
	}
	// Validate before saving. Without this, a recipe that is listed but fails in keyframe generation
	// would remain, and the cause could only be found by going back to when the script was made.
	if err := domain.ValidateVideoRecipe(sc.VideoRecipe); err != nil {
		return fmt.Errorf("recipe_save: %w", err)
	}
	return PublishingStep{}.Execute(ctx, sc)
}
