package filter

import (
	"context"
	"errors"
	"fmt"
	"testing"

	characterkit "github.com/shouni/go-character-kit/character"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// TestVideoGenerationFilterEnqueuesContinuationAfterOneCut verifies continuation task enqueueing after one cut.
func TestVideoGenerationFilterEnqueuesContinuationAfterOneCut(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, DurationSec: 8, VisualAnchor: "first"},
			{CutIndex: 2, DurationSec: 8, VisualAnchor: "second"},
		},
	}
	task := &domain.Task{
		JobID:       "job-1",
		Command:     domain.CommandMVFromKeyframeVideoRecipe,
		VideoRecipe: recipe,
	}
	queue := &captureQueue{}
	flt := VideoGenerationFilter{Runner: sequenceRunner{}}

	err := flt.Execute(context.Background(), &Context{
		State: State{
			Task:        task,
			VideoRecipe: recipe,
		},
		Services: Services{
			TaskQueue: queue,
		},
	})
	if !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("Execute() error = %v, want ErrPipelineDeferred", err)
	}
	if recipe.Cuts[0].Status != orchestrator.CutStatusGenerated {
		t.Fatalf("first cut status = %q, want %q", recipe.Cuts[0].Status, orchestrator.CutStatusGenerated)
	}
	if recipe.Cuts[1].Status == orchestrator.CutStatusGenerated {
		t.Fatalf("second cut should not be generated in the same invocation")
	}
	if len(queue.tasks) != 1 {
		t.Fatalf("enqueued tasks = %d, want 1", len(queue.tasks))
	}
	if queue.tasks[0].Command != domain.CommandVideoGenContinuation {
		t.Fatalf("continuation command = %q, want %q", queue.tasks[0].Command, domain.CommandVideoGenContinuation)
	}
	if queue.tasks[0].VideoRecipe.Cuts[0].VideoID == "" {
		t.Fatalf("continuation task did not include generated cut state")
	}
}

// TestVideoGenerationFilterPrefersDirectRunnerWhenWorkflowExists verifies the production
// path keeps using per-cut continuation even when orchestrator workflows are configured.
func TestVideoGenerationFilterPrefersDirectRunnerWhenWorkflowExists(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, DurationSec: 8, VisualAnchor: "first"},
			{CutIndex: 2, DurationSec: 8, VisualAnchor: "second"},
		},
	}
	task := &domain.Task{
		JobID:       "job-1",
		Command:     domain.CommandMVFromKeyframeVideoRecipe,
		VideoRecipe: recipe,
	}
	queue := &captureQueue{}
	workflow := &recordingVideoWorkflow{}
	flt := VideoGenerationFilter{Runner: sequenceRunner{}}

	err := flt.Execute(context.Background(), &Context{
		State: State{
			Task:        task,
			VideoRecipe: recipe,
		},
		Services: Services{
			TaskQueue: queue,
			Workflows: &orchestrator.Workflows{Video: workflow},
		},
	})
	if !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("Execute() error = %v, want ErrPipelineDeferred", err)
	}
	if workflow.runCalled {
		t.Fatal("workflow video runner was called; want direct per-cut runner")
	}
	if len(queue.tasks) != 1 {
		t.Fatalf("enqueued tasks = %d, want 1", len(queue.tasks))
	}
}

// TestVideoGenerationFilterContinuationFromShortSectionUsesContinuationCommand verifies that
// deferring a short_video_from_section task does not re-trigger section_select on resume
// (which would reset already-generated cuts back to pending) or cut_keyframe_gen/zip_upload
// (which would discard the keyframes reused from the original job).
func TestVideoGenerationFilterContinuationFromShortSectionUsesContinuationCommand(t *testing.T) {
	sectionIndex := 0
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, DurationSec: 8, VisualAnchor: "first"},
			{CutIndex: 2, DurationSec: 8, VisualAnchor: "second"},
		},
	}
	task := &domain.Task{
		JobID:        "short-1",
		Command:      domain.CommandShortVideoFromSection,
		SectionIndex: &sectionIndex,
		VideoRecipe:  recipe,
	}
	queue := &captureQueue{}
	flt := VideoGenerationFilter{Runner: sequenceRunner{}}

	err := flt.Execute(context.Background(), &Context{
		State: State{
			Task:        task,
			VideoRecipe: recipe,
		},
		Services: Services{
			TaskQueue: queue,
		},
	})
	if !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("Execute() error = %v, want ErrPipelineDeferred", err)
	}
	if len(queue.tasks) != 1 {
		t.Fatalf("enqueued tasks = %d, want 1", len(queue.tasks))
	}
	if queue.tasks[0].Command != domain.CommandVideoGenContinuation {
		t.Fatalf("continuation command = %q, want %q", queue.tasks[0].Command, domain.CommandVideoGenContinuation)
	}
}

// TestVideoGenerationFilterAddsOutputPathToContext verifies that video generation receives the output path through context.
func TestVideoGenerationFilterAddsOutputPathToContext(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, DurationSec: 8, VisualAnchor: "first"},
		},
	}
	task := &domain.Task{
		JobID:       "job-1",
		Command:     domain.CommandMVFromKeyframeVideoRecipe,
		VideoRecipe: recipe,
	}
	runner := &contextCaptureRunner{}
	flt := VideoGenerationFilter{Runner: runner}

	err := flt.Execute(context.Background(), &Context{
		State: State{
			Task:        task,
			VideoRecipe: recipe,
			OutputPath:  "gs://bucket/ap-mv/veo/jobs/job-1/",
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if runner.baseURI != "gs://bucket/ap-mv/veo/jobs/job-1/" {
		t.Fatalf("video output base URI = %q", runner.baseURI)
	}
}

// TestVideoGenerationFilterExpandsUnsupportedDurations verifies that cuts longer than Veo's
// supported durations are split into sub-cuts before generation in the full MV flow.
func TestVideoGenerationFilterExpandsUnsupportedDurations(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, DurationSec: 10, VisualAnchor: "long cut", KeyframeReference: "gs://bucket/jobs/job-1/images/cut_1.png"},
		},
	}
	task := &domain.Task{
		JobID:       "job-1",
		Command:     domain.CommandMVFromKeyframeVideoRecipe,
		VideoRecipe: recipe,
	}
	flt := VideoGenerationFilter{Runner: sequenceRunner{}}

	err := flt.Execute(context.Background(), &Context{
		State: State{
			Task:        task,
			VideoRecipe: recipe,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(recipe.Cuts) != 2 {
		t.Fatalf("cuts = %d, want 2 (10s cut split into 8s + 4s)", len(recipe.Cuts))
	}
	for i, wantDuration := range []float64{8, 4} {
		if recipe.Cuts[i].DurationSec != wantDuration {
			t.Errorf("cut[%d] duration = %v, want %v", i, recipe.Cuts[i].DurationSec, wantDuration)
		}
		if recipe.Cuts[i].Status != orchestrator.CutStatusGenerated {
			t.Errorf("cut[%d] status = %q, want generated", i, recipe.Cuts[i].Status)
		}
	}
}

// TestVideoSeedUsesCharacterSeed verifies Veo generation always receives the cut's character
// seed (the same value keyframe image generation uses, per go-veo-orchestrator's
// keyframe.Generator), regardless of any legacy recipe-level seed that might still be present
// on an already-decoded recipe. Without any seed, independent chain-start generations drift in
// appearance (costume/eye color), so the character seed is reused as the video seed. Recipe-level
// seed (MusicRecipe.Seed) is a removed legacy migration path (see domain.applyLegacyVideoRecipeFields)
// that this filter deliberately no longer reads, since honoring it caused keyframe generation
// (always character-seeded) and video generation to silently diverge on legacy-migrated recipes.
func TestVideoSeedUsesCharacterSeed(t *testing.T) {
	charSeed := int64(208222065)
	characters := &characterkit.Characters{
		ByID: map[string]*characterkit.Character{
			"tsumugi": {ID: "tsumugi", Seed: &charSeed},
		},
	}

	recipeSeed := int64(777)
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, DurationSec: 8, VisualAnchor: "a", CharacterID: "tsumugi"},
		},
	}
	// Seed is a field promoted from the embedded lyria.AIModels struct, so it cannot be set
	// in the MusicRecipe struct literal above. Setting it here simulates a recipe carrying a
	// stale legacy seed value that must now be ignored in favor of the character seed.
	recipe.MusicRecipe.Seed = &recipeSeed
	task := &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe}
	runner := &seedCaptureRunner{}
	flt := VideoGenerationFilter{Runner: runner}

	err := flt.Execute(context.Background(), &Context{
		State: State{
			Task:        task,
			VideoRecipe: recipe,
		},
		Services: Services{
			Characters: characters,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner.seeds) != 1 || runner.seeds[0] != charSeed {
		t.Errorf("seeds = %v, want [%d] (character seed, ignoring stale recipe-level seed)", runner.seeds, charSeed)
	}
}

// TestVideoGenerationFilterForcesEightSecondsForReferenceToVideo reproduces job
// short-20260711-225010-d12ddd0479ed's failure: a cut whose character has reference art was
// submitted to Veo's reference_to_video (referenceImages) with a snapped image_to_video duration
// (6s), which Vertex AI rejects with "Unsupported output video duration 6 seconds, supported
// durations are [8] for feature reference_to_video". A cut naturally 6s long (from the recipe's
// section timing) must instead be forced to Veo's only reference_to_video duration, 8s.
func TestVideoGenerationFilterForcesEightSecondsForReferenceToVideo(t *testing.T) {
	characters := &characterkit.Characters{
		ByID: map[string]*characterkit.Character{
			"tsumugi": {ID: "tsumugi", ReferenceURL: "gs://bucket/characters/tsumugi.png"},
		},
	}
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 5, StartSec: 30, DurationSec: 6, VisualAnchor: "a", CharacterID: "tsumugi"},
		},
	}
	task := &domain.Task{JobID: "job-1", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe}
	runner := &durationCaptureRunner{supportsReferenceImages: true}
	flt := VideoGenerationFilter{Runner: runner}

	if err := flt.Execute(context.Background(), &Context{
		State: State{
			Task:        task,
			VideoRecipe: recipe,
		},
		Services: Services{
			Characters: characters,
		},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner.durations) != 1 || runner.durations[0] != 8 {
		t.Errorf("durations = %v, want [8] (reference_to_video only supports 8s)", runner.durations)
	}

	// A character without reference art, or a runner that doesn't support referenceImages,
	// must keep the ordinary image_to_video snapping ({4,6,8}) and leave 6s untouched.
	recipe2 := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			{CutIndex: 5, StartSec: 30, DurationSec: 6, VisualAnchor: "a"},
		},
	}
	task2 := &domain.Task{JobID: "job-2", Command: domain.CommandMVFromKeyframeVideoRecipe, VideoRecipe: recipe2}
	runner2 := &durationCaptureRunner{supportsReferenceImages: true}
	flt2 := VideoGenerationFilter{Runner: runner2}

	if err := flt2.Execute(context.Background(), &Context{
		State: State{
			Task:        task2,
			VideoRecipe: recipe2,
		},
		Services: Services{
			Characters: characters,
		},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner2.durations) != 1 || runner2.durations[0] != 6 {
		t.Errorf("durations = %v, want [6] (no reference art, image_to_video keeps 6s)", runner2.durations)
	}
}

type durationCaptureRunner struct {
	supportsReferenceImages bool
	supportsLastFrame       bool
	durations               []float64
	requests                []ports.VideoGenerationRequest
}

// Run records each request and returns a fixed successful response.
func (r *durationCaptureRunner) Run(_ context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	r.durations = append(r.durations, req.DurationSec)
	r.requests = append(r.requests, req)
	return &ports.VideoResponse{
		CloudURL: fmt.Sprintf("gs://bucket/cut_%d.mp4", req.CutIndex),
		VideoID:  fmt.Sprintf("video-%d", req.CutIndex),
		CutIndex: req.CutIndex,
	}, nil
}

// SupportsReferenceImages implements ports.ReferenceImagesSupporter.
func (r *durationCaptureRunner) SupportsReferenceImages() bool {
	return r.supportsReferenceImages
}

// SupportsLastFrame implements ports.LastFrameSupporter.
func (r *durationCaptureRunner) SupportsLastFrame() bool {
	return r.supportsLastFrame
}

type seedCaptureRunner struct {
	seeds []int64
}

// Run records each request's seed and returns a fixed successful response.
func (r *seedCaptureRunner) Run(_ context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	r.seeds = append(r.seeds, req.Seed)
	return &ports.VideoResponse{
		CloudURL: fmt.Sprintf("gs://bucket/cut_%d.mp4", req.CutIndex),
		VideoID:  fmt.Sprintf("video-%d", req.CutIndex),
		CutIndex: req.CutIndex,
	}, nil
}

type indexedURLRunner struct{}

// Run returns a per-cut-index video URL/ID so tests can assert exactly which cut's video
// was referenced downstream (e.g. as ExtractLastFrame's input).
func (indexedURLRunner) Run(_ context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	return &ports.VideoResponse{
		CloudURL: fmt.Sprintf("gs://bucket/cut_%d.mp4", req.CutIndex),
		VideoID:  fmt.Sprintf("video-%d", req.CutIndex),
		CutIndex: req.CutIndex,
	}, nil
}

type sequenceRunner struct{}

// Run starts the receiver workflow.
func (sequenceRunner) Run(_ context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	return &ports.VideoResponse{
		CloudURL: "gs://bucket/cut.mp4",
		VideoID:  "video-id",
		CutIndex: req.CutIndex,
	}, nil
}

type contextCaptureRunner struct {
	baseURI string
}

// Run starts the receiver workflow.
func (r *contextCaptureRunner) Run(ctx context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	r.baseURI, _ = ports.VideoOutputBaseURIFromContext(ctx)
	return &ports.VideoResponse{
		CloudURL: "gs://bucket/cut.mp4",
		VideoID:  "video-id",
		CutIndex: req.CutIndex,
	}, nil
}

type captureQueue struct {
	tasks []*domain.Task
}

// Enqueue adds a task to the queue.
func (q *captureQueue) Enqueue(_ context.Context, task *domain.Task) error {
	copied := *task
	q.tasks = append(q.tasks, &copied)
	return nil
}

type recordingVideoWorkflow struct {
	runCalled bool
}

func (w *recordingVideoWorkflow) Run(_ context.Context, _ *orchestrator.VideoRecipe) ([]*orchestrator.VideoResponse, error) {
	w.runCalled = true
	return nil, nil
}

func (w *recordingVideoWorkflow) RunAndSave(_ context.Context, _ *orchestrator.VideoRecipe, _ string) (*orchestrator.VideoPlotResponse, error) {
	w.runCalled = true
	return nil, nil
}
