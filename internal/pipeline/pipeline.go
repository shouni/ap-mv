// Package pipeline provides the video-generation worker's pipeline runner, which runs Steps in order.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/shouni/gcp-kit/worker"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-remote-io/remoteio"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/go-utils/slogctx"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/pipeline/step"
	"github.com/shouni/ap-mv/internal/ports"
)

// notificationTimeout bounds one notification. Lifecycle has its own limit for recording
// the outcome and notifying as a whole (DefaultFinishTimeout).
const notificationTimeout = 10 * time.Second

// Dependencies bundles what Runner needs to run. New validates that required dependencies
// are present at construction, so a missing one is not discovered only at run time.
type Dependencies struct {
	VideoRunner       ports.VideoRunner
	TaskQueue         ports.TaskQueue
	Reader            orchestrator.ContentReader
	Writer            remoteio.Writer
	Characters        *characterkit.Characters
	HistoryRepository ports.HistoryRepository
	// WorkflowResolver resolves the orchestrator Workflows for the task.
	WorkflowResolver WorkflowResolver
	// Planner decides the execution plan (step sequence) per task.
	// When unset, the zero-value DefaultPlanner is used (optional).
	Planner Planner
	// Notifier sends completion and error notices. When unset, nothing is notified (optional).
	Notifier ports.Notifier
	// OutputBaseURI is the base URI for task artifacts. When empty, the steps do not
	// specify where to save (optional).
	OutputBaseURI string
	// Timeout is the run-time limit of one task. 0 or less means unlimited (optional).
	// It applies separately to each continuation task of a cut-split job.
	Timeout time.Duration
	// JobStatus is where job progress is recorded. When unset, status recording and the
	// re-run guard are disabled (optional).
	JobStatus ports.JobStatusStore
}

// Runner is the worker pipeline that passes a domain.Task through MusicRecipe generation,
// keyframe generation, video generation and publishing in order.
type Runner struct {
	deps Dependencies
}

// New validates the dependencies and builds a Runner. It returns an error when a required
// dependency is missing, catching misconfiguration at startup rather than at run time.
func New(deps Dependencies) (*Runner, error) {
	var missing []string
	for _, dep := range []struct {
		name string
		ok   bool
	}{
		{"VideoRunner", deps.VideoRunner != nil},
		{"TaskQueue", deps.TaskQueue != nil},
		{"Reader", deps.Reader != nil},
		{"Writer", deps.Writer != nil},
		{"Characters", deps.Characters != nil},
		{"HistoryRepository", deps.HistoryRepository != nil},
		{"WorkflowResolver", deps.WorkflowResolver != nil},
	} {
		if !dep.ok {
			missing = append(missing, dep.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("pipeline runner missing required dependencies: %s", strings.Join(missing, ", "))
	}
	if deps.Planner == nil {
		deps.Planner = DefaultPlanner{}
	}
	return &Runner{deps: deps}, nil
}

// Execute satisfies worker.TaskExecutor.
//
// The job's lifecycle (redelivery guard → validation → run → recording the outcome) belongs to
// gcp-kit/worker.Lifecycle; this only supplies what goes in each stage (the worker conventions
// in public-docs). Recovering panics, the run-time limit and detaching the ctx for recording the
// outcome are the library's job and are not written here.
// task is taken by value to match the TaskExecutor signature and turned into a pointer when passed to run.
func (r *Runner) Execute(ctx context.Context, task domain.Task) error {
	return r.lifecycle().Execute(ctx, task)
}

// lifecycle fills each stage of the job's lifecycle with this app's contents.
func (r *Runner) lifecycle() worker.Lifecycle[domain.Task, *runResult] {
	// The recorder is taken from Dependencies every time, so recording works even when a test
	// builds Runner as a literal; constructing a Recorder is cheap.
	status := newStatusRecorder(r.deps.JobStatus)
	return worker.Lifecycle[domain.Task, *runResult]{
		// Put job_id / command on every log line this job emits from here on, so each
		// step's logs can be followed per job.
		// A continuation task (video_gen_continuation) inherits the same job_id, so the
		// whole cut-split chain is grouped under one job_id.
		Prepare: func(ctx context.Context, task domain.Task) context.Context {
			return slogctx.With(ctx,
				slog.String("job_id", task.JobID),
				slog.String("command", string(task.Command)),
			)
		},
		Labels: func(task domain.Task) map[string]string {
			return map[string]string{"job_id": task.JobID, "command": string(task.Command)}
		},
		// Redelivery guard. Returning an error even once (a failed notification, say) causes a
		// redelivery, so without stopping here the Veo generation cost is paid twice. If the job is
		// unfinished, running is recorded here (before input validation, so every attempt shows up in
		// Attempts). Without being able to read the status nothing can be decided, so return an error without running.
		Begin: func(ctx context.Context, task domain.Task) (bool, error) {
			return status.begin(ctx, &task)
		},
		// If the request itself is invalid, redelivering fails on the same line (Lifecycle wraps it as Permanent).
		Validate: func(task domain.Task) error { return task.Validate() },
		Run: func(ctx context.Context, task domain.Task) (*runResult, error) {
			return r.run(ctx, &task)
		},
		Finish: func(ctx context.Context, task domain.Task, result *runResult, cause error) error {
			return r.recordOutcome(ctx, &task, status, result, cause)
		},
		// Keep it shorter than Cloud Tasks' dispatch deadline. If longer, Cloud Tasks cuts the
		// request first and the app loses its chance to write its own failure.
		Timeout: r.deps.Timeout,
	}
}

// recordOutcome records the terminal state and notifies along the same path whether the run
// succeeded or failed (Lifecycle's Finish).
//
// When the cut-off itself is the reason for the end (Timeout firing, or the request being
// cancelled for exceeding Cloud Tasks' dispatch deadline), the caller's ctx is already Done.
// Lifecycle does the detaching and both success and failure go through here, so forgetting to
// detach on only one side cannot be expressed.
func (r *Runner) recordOutcome(ctx context.Context, task *domain.Task, status statusRecorder, result *runResult, cause error) error {
	req := notificationRequest(task, result)

	switch {
	case cause != nil:
		status.markFailed(ctx, task, cause, resultVideoRecipe(result))
		r.notifyError(ctx, cause, req)
		return cause

	case result != nil && result.deferred:
		// A continuation task carries the work on under the same job_id, so marking succeeded here
		// would trigger the redelivery guard midway and the remaining cuts would never be generated.
		// Use exactly the same check as the one that suppresses the completion notice.
		return nil

	default:
		// Record, then notify. Never tell a person a job succeeded when it could not be recorded.
		status.markSucceeded(ctx, task, req, resultVideoRecipe(result))
		r.notifyComplete(ctx, req)
		return nil
	}
}

// resultVideoRecipe takes out the recipe after the run. When the step sequence failed midway
// it is nil, and the headline stays as previously recorded.
func resultVideoRecipe(result *runResult) *domain.VideoRecipe {
	if result == nil {
		return nil
	}
	return result.videoRecipe
}

// Close releases the run-time resources Runner holds.
//
// Once construction succeeds, Runner owns the VideoRunner's lifecycle (it is released via
// app.Container.Close → Pipeline.Close; builder.BuildContainer releases it only when construction fails midway).
func (r *Runner) Close() error {
	if r == nil || r.deps.VideoRunner == nil {
		return nil
	}
	if closer, ok := r.deps.VideoRunner.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Run runs the step sequence for the task and returns the processed MusicRecipe.
//
// The Cloud Tasks worker calls it through Execute, but Run is kept as the core method for
// tests and synchronous runs that want to inspect the pipeline result.
func (r *Runner) Run(ctx context.Context, task *domain.Task) (*domain.MusicRecipe, error) {
	if err := task.Validate(); err != nil {
		return nil, err
	}
	result, err := r.run(ctx, task)
	if result == nil {
		return nil, err
	}
	return result.recipe, err
}

type runResult struct {
	recipe      *domain.MusicRecipe
	videoRecipe *domain.VideoRecipe
	outputPath  string
	deferred    bool
}

func (r *Runner) run(ctx context.Context, task *domain.Task) (*runResult, error) {
	workflows, err := r.resolveWorkflows(ctx, task)
	if err != nil {
		return nil, err
	}
	videoRunner := ports.DeriveVideoRunner(r.deps.VideoRunner, task.VeoModel, task.VeoAspectRatio)
	sc := &step.Context{
		Task:              task,
		Recipe:            task.Recipe,
		VideoRecipe:       task.VideoRecipe,
		OutputPath:        r.outputPath(task),
		VideoRunner:       videoRunner,
		TaskQueue:         r.deps.TaskQueue,
		Workflows:         workflows,
		Reader:            r.deps.Reader,
		Writer:            r.deps.Writer,
		Characters:        r.deps.Characters,
		HistoryRepository: r.deps.HistoryRepository,
	}
	// New has already filled deps.Planner with DefaultPlanner{}, so it is never nil here.
	steps, err := r.deps.Planner.Plan(task, videoRunner)
	if err != nil {
		return nil, err
	}
	for _, st := range steps {
		if err := st.Execute(ctx, sc); err != nil {
			if errors.Is(err, step.ErrPipelineDeferred) {
				return newRunResult(sc, true), nil
			}
			return nil, fmt.Errorf("step %s: %w", st.Name(), err)
		}
	}
	return newRunResult(sc, false), nil
}

// newRunResult builds the result from the Context after the steps ran.
// The VideoRecipe is normalised here (as part of the pipeline run) so that building the
// notification data (notificationRequest) has no side effect on the run result.
func newRunResult(sc *step.Context, deferred bool) *runResult {
	if sc.VideoRecipe != nil {
		sc.VideoRecipe.Normalize()
	}
	return &runResult{
		recipe:      sc.Recipe,
		videoRecipe: sc.VideoRecipe,
		outputPath:  sc.OutputPath,
		deferred:    deferred,
	}
}

func (r *Runner) notifyComplete(ctx context.Context, req domain.NotificationRequest) {
	if r == nil || r.deps.Notifier == nil {
		return
	}
	notifyCtx, cancel := notificationContext(ctx)
	defer cancel()
	if err := r.deps.Notifier.NotifyTaskComplete(notifyCtx, req); err != nil {
		slog.ErrorContext(ctx, "failed to send completion notification", "job_id", req.JobID, "error", err)
	}
}

func (r *Runner) notifyError(ctx context.Context, errDetail error, req domain.NotificationRequest) {
	if r == nil || r.deps.Notifier == nil {
		return
	}
	notifyCtx, cancel := notificationContext(ctx)
	defer cancel()
	if err := r.deps.Notifier.NotifyTaskError(notifyCtx, errDetail, req); err != nil {
		slog.ErrorContext(ctx, "failed to send error notification", "job_id", req.JobID, "error", err)
	}
}

// notificationContext bounds one notification. Lifecycle hands in a ctx already detached
// from the caller, so it is not detached here.
func notificationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, notificationTimeout)
}

func notificationRequest(task *domain.Task, result *runResult) domain.NotificationRequest {
	if task == nil {
		return domain.NotificationRequest{}
	}
	historyJobID := task.JobID
	if task.OriginalJobID != "" {
		historyJobID = task.OriginalJobID
	}
	req := domain.NotificationRequest{
		JobID:        task.JobID,
		HistoryJobID: historyJobID,
		Command:      string(task.Command),
		SourceURL:    task.SourceURL,
		RecipeURL:    task.RecipeURL,
		AudioURL:     task.AudioURL,
		CharacterID:  task.CharacterID,
		VisualMode:   task.VisualMode,
		TextModel:    task.TextModel,
		ImageModel:   task.ImageModel,
		CreatedAt:    task.CreatedAt,
	}
	if result != nil {
		// Script-only jobs are saved under the same jobs prefix as finished ones, so the link target is shared.
		req.OutputURI = result.outputPath
		// videoRecipe was normalised in newRunResult. Only read it here.
		if result.videoRecipe != nil {
			req.Title = result.videoRecipe.MusicRecipe.Title
			req.CutCount = len(result.videoRecipe.Cuts)
		}
	}
	if req.Title == "" && task.Recipe != nil {
		req.Title = task.Recipe.Title
	}
	return req
}

// resolveWorkflows delegates resolving the task's Workflows to WorkflowResolver.
// When no Resolver is set, it runs without Workflows (VideoRunner directly only).
func (r *Runner) resolveWorkflows(ctx context.Context, task *domain.Task) (*orchestrator.Workflows, error) {
	if r == nil || r.deps.WorkflowResolver == nil {
		return nil, nil
	}
	return r.deps.WorkflowResolver.Resolve(ctx, task)
}

// outputPath returns the base path where task artifacts go.
//
// When OutputBaseURI is unset, the steps do not specify where to save, so it returns an empty string.
func (r *Runner) outputPath(task *domain.Task) string {
	if task == nil || strings.TrimSpace(r.deps.OutputBaseURI) == "" {
		return ""
	}
	return strings.TrimRight(r.deps.OutputBaseURI, "/") + "/" + task.JobID + "/"
}
