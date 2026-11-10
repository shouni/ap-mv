package step

import (
	"context"
	"fmt"
)

// OriginalJobOutputStep points the output of the following steps back at the **original job**'s root.
//
// Regeneration tasks mint a new JobID every time so Cloud Tasks task names never collide, and
// sc.OutputPath becomes that new job's path. For operations that keep the remade artifact as a
// separate, standalone piece (regenerate_cut_video / short_video_from_section) that is correct.
//
// section_video and finalize_video, on the other hand, complete one MV a little at a time. If
// outputs scattered into a separate directory per task, the finished MV would really be
// "metadata in the original job, videos in a different job per run", leaving garbage that deleting
// the original job would not clean up. So for these commands the output is first pointed back at
// the original job here, gathering keyframes, videos, Veo usage and metadata in one place.
type OriginalJobOutputStep struct{}

// Name returns the receiver name.
func (OriginalJobOutputStep) Name() string { return "original_job_output" }

// Execute repoints sc.OutputPath at the job the task's recipe came from.
func (OriginalJobOutputStep) Execute(_ context.Context, sc *Context) error {
	if sc == nil || sc.Task == nil {
		return fmt.Errorf("original_job_output requires task")
	}
	outputPath := originalJobOutputPath(sc.Task.RecipeURL)
	if outputPath == "" {
		return fmt.Errorf("original_job_output: cannot resolve original job output path from recipe_url %q", sc.Task.RecipeURL)
	}
	sc.OutputPath = outputPath
	return nil
}
