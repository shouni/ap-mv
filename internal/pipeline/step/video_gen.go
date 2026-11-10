package step

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
	veorunner "github.com/shouni/go-veo-orchestrator/runner"
	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// VideoGenerationStep is the pipeline step that generates the actual videos from a VideoRecipe.
type VideoGenerationStep struct {
	Runner ports.VideoRunner
	// UsePreviousVideo reflects the VEO_USE_PREVIOUS_VIDEO setting.
	// When true, every cut after the first is normalised to the video_extension duration (fixed at 7 seconds).
	UsePreviousVideo bool
	// VideoProcessor is used to extract the previous chain's last frame on a chain reset.
	// When nil, frame extraction is skipped and generation uses the static character reference image.
	VideoProcessor ports.VideoProcessor
	// SectionScoped, when true, narrows generation to the cuts belonging to the section at
	// sc.Task.SectionIndex (the section_video command). **Nothing is removed from the recipe.**
	// Cuts outside the target are skipped rather than trimmed because the later Publishing step
	// saves this same recipe, and saving it trimmed would simply delete the other sections' cuts.
	// Continuation tasks carry the recipe in the payload (see enqueueContinuation), so a recipe
	// trimmed once would stay trimmed to the end.
	SectionScoped bool
}

// Name returns the receiver name.
func (VideoGenerationStep) Name() string { return "video_gen" }

// Execute runs the receiver processing step.
func (f VideoGenerationStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil || sc.Task == nil {
		return fmt.Errorf("video generation requires task and recipe")
	}
	ctx = videoOutputContext(ctx, sc)
	if err := ensureVideoRecipe(sc); err != nil {
		return err
	}
	applyTaskAudioURLToVideoRecipe(sc.Task, sc.VideoRecipe)
	// ExpandCutsToSupportedDurations decides a cut's section directly from cut.SectionIndex, so
	// make sure it is filled in here (in case the caller has not normalised yet). Normalize is
	// idempotent, so calling it again is harmless.
	sc.VideoRecipe.Normalize()
	// Cuts with a duration Veo does not support (anything but 8 seconds for reference_to_video,
	// anything but 4/6/8 seconds for image_to_video) are split or rounded before generation. Generated
	// cuts are left unchanged so their metadata does not drift from the actual video length. When
	// usePreviousVideo is true, the chain is reset automatically just before the cumulative
	// video_extension duration reaches Veo's limit (veo.ContinuationMaxDurationSec)
	// (see the comment on veo.ExpandCutsToSupportedDurations for details).
	sc.VideoRecipe.Cuts = veo.ExpandCutsToSupportedDurations(sc.VideoRecipe.Cuts, f.UsePreviousVideo, sc.Characters, referenceImagesSupported(f.resolvedVideoRunner(sc)))
	// Order of execution strategies: (1) when a VideoRunner is set, run it directly first (the
	// resumable approach that generates one cut at a time and, when cuts remain, enqueues a
	// continuation task and stops). (2) Without a VideoRunner but with an orchestrator workflow,
	// delegate generating every cut to it (not resumable; it processes every cut internally in one
	// go). Since go-veo-orchestrator v1.7.0, Workflows.Video is never nil even when no VideoRunner is
	// set — it always holds a non-nil runner (a dummy that returns ErrVideoRunnerNotConfigured when
	// unset) — so a nil check cannot tell. Call it and look at the error.
	// (3) When there is no orchestrator workflow at all, call runDirect, which returns the runner-not-set error.
	if f.hasVideoRunner(sc) {
		return f.runDirect(ctx, sc)
	}
	if sc.Workflows != nil {
		return f.runWithWorkflow(ctx, sc)
	}
	return f.runDirect(ctx, sc)
}

// resolvedVideoRunner returns the VideoRunner that will actually be used for generation:
// f.Runner takes priority, falling back to sc.VideoRunner.
func (f VideoGenerationStep) resolvedVideoRunner(sc *Context) ports.VideoRunner {
	if f.Runner != nil {
		return f.Runner
	}
	return sc.VideoRunner
}

// referenceImagesSupported reports whether the VideoRunner actually used supports Veo's
// referenceImages (reference_to_video, fixed at 8 seconds). When the Runner does not implement
// ports.ReferenceImagesSupporter (test doubles and the like) it returns false and falls back to
// rounding to {4,6,8} seconds for image_to_video.
func referenceImagesSupported(runner ports.VideoRunner) bool {
	return ports.RunnerCapabilities(runner).ReferenceImages
}

func (f VideoGenerationStep) hasVideoRunner(sc *Context) bool {
	if f.Runner != nil {
		return true
	}
	return sc != nil && sc.VideoRunner != nil
}

// runWithWorkflow delegates cut generation to the orchestrator workflow.
// The workflow handles all cuts internally, so deferred continuation is not required.
func (f VideoGenerationStep) runWithWorkflow(ctx context.Context, sc *Context) error {
	if _, err := sc.Workflows.Video.Run(ctx, sc.VideoRecipe); err != nil {
		if errors.Is(err, orchestrator.ErrVideoRunnerNotConfigured) {
			return fmt.Errorf("video generation requires a VideoRunner (neither a direct runner nor an orchestrator workflow VideoRunner is configured): %w", err)
		}
		return err
	}
	recipe, err := toDomainRecipe(sc.VideoRecipe)
	sc.Recipe = recipe
	return err
}

// runDirect generates cuts one by one via VideoRunner.
// After each cut it enqueues a continuation task and defers when cuts remain,
// allowing Cloud Tasks to stay within its execution time limit.
func (f VideoGenerationStep) runDirect(ctx context.Context, sc *Context) error {
	applyTaskCharacterIDToVideoRecipe(sc.Task, sc.VideoRecipe)
	sc.VideoRecipe.Normalize()
	videoRunner := f.resolvedVideoRunner(sc)
	if videoRunner == nil {
		return fmt.Errorf("video runner is not configured")
	}

	inScope, err := f.cutScope(sc)
	if err != nil {
		return err
	}

	timeline := veorunner.NewVideoTimelineRunner(videoRunner).
		WithRequestBuilder(videoRequestBuilder{characters: sc.Characters, usePreviousVideo: f.UsePreviousVideo}).
		WithCutGate(f.beforeCut(sc, inScope)).
		WithCutObserver(f.afterCut(sc, inScope))

	if _, err := timeline.Run(ctx, sc.VideoRecipe); err != nil {
		// Stopping after dispatching a continuation task is not a failure. Run wraps the observer's
		// error as "stopped while post-processing cut N", so returning it as-is would record a failure
		// and mark the job failed even though a continuation task will resume it. Return only the marker.
		if errors.Is(err, ErrPipelineDeferred) {
			return ErrPipelineDeferred
		}
		return err
	}

	domainRecipe, err := toDomainRecipe(sc.VideoRecipe)
	if err != nil {
		return err
	}
	sc.Recipe = domainRecipe
	return nil
}

// beforeCut returns a runner.CutGate gathering the decisions made just before generating one cut.
//
// Its job is to skip cuts of sections outside its scope and to replace the reference image at a
// chain's start with the previous chain's last frame. Treating what lies beyond a skipped cut as
// a chain start (cutting the hand-over source and marking it) is the library's job, not done here.
func (f VideoGenerationStep) beforeCut(sc *Context, inScope func(int) bool) veorunner.CutGate {
	return func(ctx context.Context, recipe *video.Recipe, i int) (bool, error) {
		cut := &recipe.Cuts[i]
		if !inScope(cut.SectionIndex) {
			return false, nil
		}
		// i > 0 means "not the first chain in the job" = a chain was actually generated just before.
		// Its last frame is handed over as the next chain's reference image, which curbs the visual
		// wobble (costume drift and the like) of generating independently from static character art.
		// However, IsSectionStart marks an "intentional scene change" (either an actual section
		// boundary of the song or a reset within a scene by scene_split.go; see the comment on
		// veo.ExpandCutsToSupportedDurations for details), so for either reason the previous picture
		// is not handed over and the cut is generated from its own assigned keyframe reference.
		if f.UsePreviousVideo && cut.IsChainStart && !cut.IsSectionStart && i > 0 {
			if err := f.applyChainResetKeyframe(ctx, sc, cut, recipe.Cuts[i-1].VideoURL); err != nil {
				return false, err
			}
		}
		return true, nil
	}
}

// afterCut returns a runner.CutObserver gathering the post-processing after each cut is generated.
//
// It is not called for generated cuts, so the billing records and colour correction of past
// cuts are not re-run on every resume.
func (f VideoGenerationStep) afterCut(sc *Context, inScope func(int) bool) veorunner.CutObserver {
	return func(ctx context.Context, cut *video.Cut, _ *video.Response) error {
		recordVeoUsage(ctx, sc, cut)
		if f.UsePreviousVideo && cut.DurationSec == veo.VideoExtensionDurationSec {
			if err := f.colorCorrectExtensionCut(ctx, sc, cut); err != nil {
				return err
			}
		}
		// If enqueueing the continuation task fails, this job stops here. The queue runs with
		// max-attempts=1 and a failed task is not redelivered, so nothing is left to generate the
		// remaining cuts (noticed by the artifact never appearing).
		// The state of generated cuts has been saved, so resubmitting the same recipe skips cuts
		// where Cut.IsGenerated() is true and resumes from the middle.
		//
		// While this is being validated, this "stop" behaviour is chosen to avoid transient failures
		// hiding systematic bugs behind retries, and to avoid regenerating a failed cut doubling the
		// Veo charge. When moving to steady-state operation, raising max-attempts will make this
		// resume automatically.
		if hasPendingCuts(sc.VideoRecipe, inScope) && sc.TaskQueue != nil {
			return enqueueContinuation(ctx, sc, cut.CutIndex)
		}
		return nil
	}
}

// enqueueContinuation persists the in-progress VideoRecipe and enqueues a
// CommandVideoGenContinuation task to resume generation of the remaining pending cuts, then
// returns ErrPipelineDeferred so the pipeline stops here instead of treating this run as complete.
//
// The continuation task is enqueued under a deterministic name derived from job ID + cutIndex
// (see continuationTaskID), so that if Cloud Tasks redelivers the current task (at-least-once
// delivery) and this function runs again for the same completed cut, the second enqueue attempt
// is rejected by Cloud Tasks as a duplicate instead of creating a second continuation task. This
// does not by itself prevent the redelivered execution from re-generating cutIndex's video (that
// would require checking persisted, authoritative state before generating each cut) — it only
// keeps a redelivery from fanning out into duplicate downstream tasks.
func enqueueContinuation(ctx context.Context, sc *Context, cutIndex int) error {
	domainRecipe, err := toDomainRecipe(sc.VideoRecipe)
	if err != nil {
		return err
	}
	sc.Recipe = domainRecipe
	nextTask := *sc.Task
	// Note the original command before overwriting Command. The continuation's plan uses it to
	// decide "whether to join", so losing it would make a section_video continuation run through the join.
	// If this is already a continuation task, carry on the OriginCommand it holds as-is.
	if sc.Task.Command != domain.CommandVideoGenContinuation {
		nextTask.OriginCommand = sc.Task.Command
	}
	nextTask.Command = domain.CommandVideoGenContinuation
	nextTask.Recipe = sc.Recipe
	nextTask.VideoRecipe = sc.VideoRecipe
	nextTask.CreatedAt = time.Now().UTC()
	if err := sc.TaskQueue.EnqueueWithName(ctx, continuationTaskID(sc.Task.JobID, cutIndex), &nextTask); err != nil {
		return fmt.Errorf("enqueue continuation after cut %d: %w", cutIndex, err)
	}
	return ErrPipelineDeferred
}

// continuationTaskID builds a deterministic Cloud Tasks task ID for the continuation enqueued
// after cutIndex finishes generating. Cloud Tasks task IDs must match [A-Za-z0-9_-]{1,500}, so
// any other character in jobID is replaced with "_" defensively.
func continuationTaskID(jobID string, cutIndex int) string {
	safeJobID := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, jobID)
	return fmt.Sprintf("%s-cont-cut-%d", safeJobID, cutIndex)
}

// ensureVideoRecipe converts sc.Recipe to sc.VideoRecipe when it is not already set.
func ensureVideoRecipe(sc *Context) error {
	if sc.VideoRecipe != nil {
		return nil
	}
	if sc.Recipe == nil {
		return fmt.Errorf("video generation requires recipe")
	}
	recipe, err := toVideoRecipe(sc.Recipe)
	if err != nil {
		return err
	}
	sc.VideoRecipe = recipe
	return nil
}

// videoOutputContext adds the output base URI to the context when available.
func videoOutputContext(ctx context.Context, sc *Context) context.Context {
	if sc == nil {
		return ctx
	}
	return ports.WithVideoOutputBaseURI(ctx, sc.OutputPath)
}

// hasPendingCuts reports whether a recipe has cuts awaiting video generation.
//
// inScope decides which cuts this run is responsible for (nil means every cut). Without
// checking the scope here, a section run would count ungenerated cuts outside its scope as
// "remaining" and dispatch a continuation task that generates nothing, one wasted run each time.
func hasPendingCuts(recipe *video.Recipe, inScope func(sectionIndex int) bool) bool {
	if recipe == nil {
		return false
	}
	for i := range recipe.Cuts {
		if recipe.Cuts[i].IsGenerated() {
			continue
		}
		if inScope == nil || inScope(recipe.Cuts[i].SectionIndex) {
			return true
		}
	}
	return false
}

// cutScope returns the predicate for the cuts this run should generate video for.
// When not SectionScoped it is a function that always returns true (= every cut).
func (f VideoGenerationStep) cutScope(sc *Context) (func(sectionIndex int) bool, error) {
	if !f.SectionScoped {
		return func(int) bool { return true }, nil
	}
	if sc.Task.SectionIndex == nil {
		return nil, fmt.Errorf("section-scoped video generation requires section_index")
	}
	sectionIndex := *sc.Task.SectionIndex
	sections := sc.VideoRecipe.MusicRecipe.Sections
	if sectionIndex < 0 || sectionIndex >= len(sections) {
		return nil, fmt.Errorf("section_index %d is out of range (recipe has %d sections)", sectionIndex, len(sections))
	}
	// cut.SectionIndex is 1-based, so add 1 when comparing with the 0-based sectionIndex
	// (the same rule as SectionSelectStep / resolveRegenTargets).
	wantSectionIndex := sectionIndex + 1

	found := false
	for i := range sc.VideoRecipe.Cuts {
		if sc.VideoRecipe.Cuts[i].SectionIndex == wantSectionIndex {
			found = true
			break
		}
	}
	// With no matching cut, it would finish normally without generating or saving anything. To the
	// person who pressed the button that looks like "it ran and nothing happened", so fail here.
	if !found {
		return nil, fmt.Errorf("no cuts found in section %d (%s)", sectionIndex, sections[sectionIndex].Name)
	}
	return func(cutSectionIndex int) bool { return cutSectionIndex == wantSectionIndex }, nil
}
