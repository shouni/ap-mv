package pipeline

import (
	"context"
	"errors"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/worker/filter"
)

type noopFilter struct{}

// Name returns the receiver name.
func (noopFilter) Name() string { return "noop" }

// Execute runs the receiver processing step.
func (noopFilter) Execute(context.Context, *filter.Context) error { return nil }

type errorFilter struct{}

func (errorFilter) Name() string { return "error" }

func (errorFilter) Execute(context.Context, *filter.Context) error { return errors.New("boom") }

type deferredFilter struct{}

func (deferredFilter) Name() string { return "deferred" }

func (deferredFilter) Execute(context.Context, *filter.Context) error {
	return filter.ErrPipelineDeferred
}

type recordingNotifier struct {
	completed []domain.NotificationRequest
	errors    []domain.NotificationRequest
}

func (n *recordingNotifier) NotifyTaskComplete(_ context.Context, req domain.NotificationRequest) error {
	n.completed = append(n.completed, req)
	return nil
}

func (n *recordingNotifier) NotifyTaskError(_ context.Context, _ error, req domain.NotificationRequest) error {
	n.errors = append(n.errors, req)
	return nil
}

type contextRecordingNotifier struct {
	errorCtxErr error
}

func (n *contextRecordingNotifier) NotifyTaskComplete(_ context.Context, _ domain.NotificationRequest) error {
	return nil
}

func (n *contextRecordingNotifier) NotifyTaskError(ctx context.Context, _ error, _ domain.NotificationRequest) error {
	n.errorCtxErr = ctx.Err()
	return nil
}

// TestDefaultFiltersForVideoRecipeCreateStopsAfterCutKeyframe verifies the video recipe creation filter chain.
func TestDefaultFiltersForVideoRecipeCreateStopsAfterCutKeyframe(t *testing.T) {
	filters := defaultFilters(domain.CommandVideoRecipeCreate, nil)

	got := make([]string, 0, len(filters))
	for _, flt := range filters {
		got = append(got, flt.Name())
	}
	want := []string{"scripting", "cut_keyframe_gen", "zip_upload"}
	if len(got) != len(want) {
		t.Fatalf("filters = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filters = %v, want %v", got, want)
		}
	}
}

// TestDefaultFiltersForLegacyComposeStillRunsFullPipeline verifies the legacy compose default filter chain.
func TestDefaultFiltersForLegacyComposeStillRunsFullPipeline(t *testing.T) {
	filters := defaultFilters(domain.CommandCompose, nil)

	got := make([]string, 0, len(filters))
	for _, flt := range filters {
		got = append(got, flt.Name())
	}
	want := []string{"scripting", "cut_keyframe_gen", "zip_upload", "video_gen", "publishing"}
	if len(got) != len(want) {
		t.Fatalf("filters = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filters = %v, want %v", got, want)
		}
	}
}

// TestDefaultFiltersForVideoGenContinuationSkipsRecipeAndKeyframeStages verifies that the
// internal continuation command only resumes video generation and publishing, since the
// carried VideoRecipe already reflects scripting/keyframe/section-select/zip-upload results
// from the original command that deferred it.
func TestDefaultFiltersForVideoGenContinuationSkipsRecipeAndKeyframeStages(t *testing.T) {
	filters := defaultFilters(domain.CommandVideoGenContinuation, nil)

	got := make([]string, 0, len(filters))
	for _, flt := range filters {
		got = append(got, flt.Name())
	}
	want := []string{"video_gen", "publishing"}
	if len(got) != len(want) {
		t.Fatalf("filters = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filters = %v, want %v", got, want)
		}
	}
}

// TestRunUsesWorkflowFactoryForSelectedModels verifies workflow creation for custom selected models.
func TestRunUsesWorkflowFactoryForSelectedModels(t *testing.T) {
	calls := 0
	runner := &Runner{
		OrchestratorConfig: orchestrator.Config{
			GeminiModel: "gemini-default",
			ImageModel:  "image-default",
		},
		Filters: []filter.Filter{noopFilter{}},
		WorkflowFactory: func(context.Context, *domain.Task) (*orchestrator.Workflows, error) {
			calls++
			return &orchestrator.Workflows{}, nil
		},
	}

	_, err := runner.Run(context.Background(), &domain.Task{
		JobID:    "job-1",
		Command:  domain.CommandCompose,
		Text:     "source",
		AIModels: domain.AIModels{TextModel: "gemini-alt", ImageModel: "image-default"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("workflow factory calls = %d, want 1", calls)
	}
}

// TestRunSkipsWorkflowFactoryForDefaultModels verifies default models reuse the runner workflows.
func TestRunSkipsWorkflowFactoryForDefaultModels(t *testing.T) {
	calls := 0
	runner := &Runner{
		OrchestratorConfig: orchestrator.Config{
			GeminiModel: "gemini-default",
			ImageModel:  "image-default",
		},
		Filters: []filter.Filter{noopFilter{}},
		WorkflowFactory: func(context.Context, *domain.Task) (*orchestrator.Workflows, error) {
			calls++
			return &orchestrator.Workflows{}, nil
		},
	}

	_, err := runner.Run(context.Background(), &domain.Task{
		JobID:    "job-1",
		Command:  domain.CommandCompose,
		Text:     "source",
		AIModels: domain.AIModels{TextModel: "gemini-default", ImageModel: "image-default"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("workflow factory calls = %d, want 0", calls)
	}
}

// TestExecuteNotifiesCompletion verifies worker execution sends a completion notification.
func TestExecuteNotifiesCompletion(t *testing.T) {
	notifier := &recordingNotifier{}
	runner := &Runner{
		Filters:       []filter.Filter{noopFilter{}},
		Workflows:     &orchestrator.Workflows{},
		OutputBaseURI: "gs://bucket/ap-mv/veo/jobs",
		Notifier:      notifier,
	}

	err := runner.Execute(context.Background(), domain.Task{
		JobID:      "job-1",
		Command:    domain.CommandVideoRecipeCreate,
		SourceURL:  "gs://bucket/music_recipe.json",
		VisualMode: "sparkle_rock",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(notifier.completed) != 1 {
		t.Fatalf("completion notifications = %d, want 1", len(notifier.completed))
	}
	if notifier.completed[0].OutputURI != "gs://bucket/ap-mv/veo/jobs/job-1/" {
		t.Fatalf("notification output URI = %q", notifier.completed[0].OutputURI)
	}
	if len(notifier.errors) != 0 {
		t.Fatalf("error notifications = %d, want 0", len(notifier.errors))
	}
}

// TestExecuteNotifiesError verifies worker execution sends an error notification.
func TestExecuteNotifiesError(t *testing.T) {
	notifier := &recordingNotifier{}
	runner := &Runner{
		Filters:  []filter.Filter{errorFilter{}},
		Notifier: notifier,
	}

	err := runner.Execute(context.Background(), domain.Task{
		JobID:     "job-1",
		Command:   domain.CommandVideoRecipeCreate,
		SourceURL: "gs://bucket/music_recipe.json",
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if len(notifier.errors) != 1 {
		t.Fatalf("error notifications = %d, want 1", len(notifier.errors))
	}
	if len(notifier.completed) != 0 {
		t.Fatalf("completion notifications = %d, want 0", len(notifier.completed))
	}
}

// TestExecuteNotifiesErrorWithDetachedContext verifies cancellation of the worker request does not cancel notification delivery.
func TestExecuteNotifiesErrorWithDetachedContext(t *testing.T) {
	notifier := &contextRecordingNotifier{}
	runner := &Runner{
		Filters:  []filter.Filter{errorFilter{}},
		Notifier: notifier,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runner.Execute(ctx, domain.Task{
		JobID:     "job-1",
		Command:   domain.CommandVideoRecipeCreate,
		SourceURL: "gs://bucket/music_recipe.json",
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if notifier.errorCtxErr != nil {
		t.Fatalf("notification context error = %v, want nil", notifier.errorCtxErr)
	}
}

// TestExecuteSkipsCompletionNotificationWhenDeferred verifies continuation tasks do not emit completion.
func TestExecuteSkipsCompletionNotificationWhenDeferred(t *testing.T) {
	notifier := &recordingNotifier{}
	runner := &Runner{
		Filters:  []filter.Filter{deferredFilter{}},
		Notifier: notifier,
	}

	err := runner.Execute(context.Background(), domain.Task{
		JobID:     "job-1",
		Command:   domain.CommandVideoRecipeCreate,
		SourceURL: "gs://bucket/music_recipe.json",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(notifier.completed) != 0 {
		t.Fatalf("completion notifications = %d, want 0", len(notifier.completed))
	}
	if len(notifier.errors) != 0 {
		t.Fatalf("error notifications = %d, want 0", len(notifier.errors))
	}
}
