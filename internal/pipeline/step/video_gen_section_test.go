package step

import (
	"context"
	"errors"
	"testing"

	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

// sectionScopedRecipe builds a recipe of 2 sections with 2 cuts each.
// cut.SectionIndex is 1-based (the same rule as Normalize / SectionSelectStep).
func sectionScopedRecipe() *video.Recipe {
	return &video.Recipe{
		MusicRecipe: video.MusicRecipe{
			Title: "test",
			Sections: []video.Section{
				{Name: "Verse", Duration: 16, StartSeconds: 0, EndSeconds: 16},
				{Name: "Chorus", Duration: 16, StartSeconds: 16, EndSeconds: 32},
			},
		},
		Cuts: []video.Cut{
			{CutIndex: 1, SectionIndex: 1, VisualAnchor: "v1", StartSec: 0, DurationSec: 8},
			{CutIndex: 2, SectionIndex: 1, VisualAnchor: "v2", StartSec: 8, DurationSec: 8},
			{CutIndex: 3, SectionIndex: 2, VisualAnchor: "c1", StartSec: 16, DurationSec: 8},
			{CutIndex: 4, SectionIndex: 2, VisualAnchor: "c2", StartSec: 24, DurationSec: 8},
		},
	}
}

func sectionScopedContext(recipe *video.Recipe, sectionIndex int, queue *captureQueue) *Context {
	return &Context{
		Task: &domain.Task{
			JobID:        "job-1",
			Command:      domain.CommandSectionVideo,
			SectionIndex: &sectionIndex,
			VideoRecipe:  recipe,
		},
		VideoRecipe: recipe,
		TaskQueue:   queue,
	}
}

// TestSectionScopedGenerationKeepsOtherSectionsInTheRecipe pins the property of this feature that
// most must not break. Cuts of sections outside the target are only "skipped" and must not be
// removed from the recipe. Continuation tasks carry the recipe in the payload and the final
// Publishing saves it as-is to the original job, so trimming here would delete the other sections' cuts beyond recovery.
func TestSectionScopedGenerationKeepsOtherSectionsInTheRecipe(t *testing.T) {
	recipe := sectionScopedRecipe()
	queue := &captureQueue{}
	st := VideoGenerationStep{Runner: sequenceRunner{}, SectionScoped: true}

	// Generate only section 2 (0-based index 1).
	err := st.Execute(context.Background(), sectionScopedContext(recipe, 1, queue))
	if err != nil && !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(recipe.Cuts) != 4 {
		t.Fatalf("recipe has %d cuts, want 4 (the untargeted section must survive)", len(recipe.Cuts))
	}
	for _, cut := range recipe.Cuts {
		if cut.SectionIndex == 1 && cut.Status == video.CutStatusGenerated {
			t.Errorf("cut %d is outside the target section but was generated", cut.CutIndex)
		}
	}
	if recipe.Cuts[2].Status != video.CutStatusGenerated {
		t.Errorf("first cut of the target section was not generated (status %q)", recipe.Cuts[2].Status)
	}
	// The recipe passed to the continuation task is not trimmed either.
	if len(queue.tasks) > 0 && len(queue.tasks[0].VideoRecipe.Cuts) != 4 {
		t.Errorf("continuation carried %d cuts, want 4", len(queue.tasks[0].VideoRecipe.Cuts))
	}
}

// TestSectionScopedGenerationStopsWhenTheSectionIsDone pins that no continuation task is
// dispatched once the section in scope is rendered. Deciding only by "are ungenerated cuts left"
// without looking at the scope would count the other sections' and throw one useless continuation every time.
func TestSectionScopedGenerationStopsWhenTheSectionIsDone(t *testing.T) {
	recipe := sectionScopedRecipe()
	// Mark the target section's cuts as generated, leaving one.
	recipe.Cuts[2].Status = video.CutStatusGenerated
	recipe.Cuts[2].VideoID = "vid-3"
	recipe.Cuts[2].VideoURL = "gs://bucket/jobs/job-1/videos/cut_3.mp4"

	queue := &captureQueue{}
	st := VideoGenerationStep{Runner: sequenceRunner{}, SectionScoped: true}

	if err := st.Execute(context.Background(), sectionScopedContext(recipe, 1, queue)); err != nil {
		t.Fatalf("Execute() error = %v, want the run to finish without deferring", err)
	}
	if len(queue.tasks) != 0 {
		t.Errorf("enqueued %d continuation tasks, want 0 (the target section is complete)", len(queue.tasks))
	}
	if recipe.Cuts[3].Status != video.CutStatusGenerated {
		t.Errorf("last cut of the target section was not generated (status %q)", recipe.Cuts[3].Status)
	}
	if recipe.Cuts[0].Status == video.CutStatusGenerated || recipe.Cuts[1].Status == video.CutStatusGenerated {
		t.Error("cuts outside the target section were generated")
	}
}

// TestSectionScopedGenerationMarksChainStartAfterASkippedGap pins that the cut right after a
// skipped gap is marked as a chain start. Without it, chain_finalize misses the boundary and drops
// the previous chain's last video from the join, so that section vanishes entirely from the
// finished video (quietly, after the generation and its cost are done).
func TestSectionScopedGenerationMarksChainStartAfterASkippedGap(t *testing.T) {
	recipe := sectionScopedRecipe()
	queue := &captureQueue{}
	st := VideoGenerationStep{Runner: sequenceRunner{}, SectionScoped: true, UsePreviousVideo: true}

	err := st.Execute(context.Background(), sectionScopedContext(recipe, 1, queue))
	if err != nil && !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("Execute() error = %v", err)
	}
	if !recipe.Cuts[2].IsChainStart {
		t.Error("the first generated cut after the skipped section is not marked as a chain start")
	}
}

// TestSectionScopedGenerationRejectsAnEmptySection pins that it errors when the target section
// has no cut at all. Finishing normally in silence would look to the person who pressed the
// button like "it ran and nothing happened".
func TestSectionScopedGenerationRejectsAnEmptySection(t *testing.T) {
	recipe := sectionScopedRecipe()
	recipe.Cuts = recipe.Cuts[:2] // remove section 2's cuts
	st := VideoGenerationStep{Runner: sequenceRunner{}, SectionScoped: true}

	if err := st.Execute(context.Background(), sectionScopedContext(recipe, 1, &captureQueue{})); err == nil {
		t.Fatal("Execute() = nil, want an error for a section with no cuts")
	}
}

// TestSectionScopedGenerationRejectsAnOutOfRangeSection pins the range check.
func TestSectionScopedGenerationRejectsAnOutOfRangeSection(t *testing.T) {
	st := VideoGenerationStep{Runner: sequenceRunner{}, SectionScoped: true}

	if err := st.Execute(context.Background(), sectionScopedContext(sectionScopedRecipe(), 9, &captureQueue{})); err == nil {
		t.Fatal("Execute() = nil, want an error for an out-of-range section_index")
	}
}

// TestContinuationRemembersTheOriginCommand pins that a continuation task keeps the original
// command. Command is overwritten with video_gen_continuation, so losing it would leave the plan
// unable to restore "whether to join" and a section_video continuation would run through the join.
func TestContinuationRemembersTheOriginCommand(t *testing.T) {
	recipe := sectionScopedRecipe()
	queue := &captureQueue{}
	st := VideoGenerationStep{Runner: sequenceRunner{}, SectionScoped: true}

	if err := st.Execute(context.Background(), sectionScopedContext(recipe, 1, queue)); !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("Execute() error = %v, want ErrPipelineDeferred", err)
	}
	if len(queue.tasks) != 1 {
		t.Fatalf("enqueued tasks = %d, want 1", len(queue.tasks))
	}
	if got := queue.tasks[0].OriginCommand; got != domain.CommandSectionVideo {
		t.Errorf("continuation OriginCommand = %q, want %q", got, domain.CommandSectionVideo)
	}

	// Even a continuation of a continuation keeps the original command (Command is already continuation, so it is not overwritten).
	next := queue.tasks[0]
	nextRecipe := next.VideoRecipe
	nextQueue := &captureQueue{}
	nextCtx := &Context{
		Task: next, VideoRecipe: nextRecipe,
		TaskQueue: nextQueue,
	}
	if err := st.Execute(context.Background(), nextCtx); err != nil && !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("continuation Execute() error = %v", err)
	}
	for _, task := range nextQueue.tasks {
		if task.OriginCommand != domain.CommandSectionVideo {
			t.Errorf("second continuation OriginCommand = %q, want it preserved", task.OriginCommand)
		}
	}
}

// TestUnscopedGenerationStillCoversEveryCut pins that a conventional run that is not SectionScoped
// keeps covering every cut (introducing the narrowing did not change the default behaviour).
func TestUnscopedGenerationStillCoversEveryCut(t *testing.T) {
	recipe := sectionScopedRecipe()
	st := VideoGenerationStep{Runner: sequenceRunner{}}
	ctx := &Context{
		Task:        &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe},
		VideoRecipe: recipe,
	}
	// Without a TaskQueue it does not escape into a continuation and generates every cut on the spot.
	if err := st.Execute(context.Background(), ctx); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, cut := range recipe.Cuts {
		if cut.Status != video.CutStatusGenerated {
			t.Errorf("cut %d status = %q, want generated", cut.CutIndex, cut.Status)
		}
	}
}
