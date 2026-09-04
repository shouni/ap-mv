package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/pipeline/step"
)

type noopStep struct{}

// Name returns the receiver name.
func (noopStep) Name() string { return "noop" }

// Execute runs the receiver processing step.
func (noopStep) Execute(context.Context, *step.Context) error { return nil }

type errorStep struct{}

func (errorStep) Name() string { return "error" }

func (errorStep) Execute(context.Context, *step.Context) error { return errors.New("boom") }

type deferredStep struct{}

func (deferredStep) Name() string { return "deferred" }

func (deferredStep) Execute(context.Context, *step.Context) error {
	return step.ErrPipelineDeferred
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

// TestNewReportsMissingDependencies verifies that construction fails fast, naming every
// missing required dependency instead of deferring the failure to task execution.
func TestNewReportsMissingDependencies(t *testing.T) {
	_, err := New(Dependencies{})
	if err == nil {
		t.Fatal("New() error = nil, want error for missing dependencies")
	}
	for _, name := range []string{"VideoRunner", "TaskQueue", "Reader", "Writer", "Characters", "HistoryRepository", "WorkflowResolver"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("New() error = %q, want mention of %s", err, name)
		}
	}
}

// TestExecuteNotifiesCompletion verifies worker execution sends a completion notification.
func TestExecuteNotifiesCompletion(t *testing.T) {
	notifier := &recordingNotifier{}
	runner := &Runner{deps: Dependencies{
		Planner:          StaticPlanner{noopStep{}},
		WorkflowResolver: StaticWorkflowResolver{Workflows: &orchestrator.Workflows{}},
		OutputBaseURI:    "gs://bucket/ap-mv/veo/jobs",
		Notifier:         notifier,
	}}

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
	runner := &Runner{deps: Dependencies{
		Planner:  StaticPlanner{errorStep{}},
		Notifier: notifier,
	}}

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
	runner := &Runner{deps: Dependencies{
		Planner:  StaticPlanner{errorStep{}},
		Notifier: notifier,
	}}
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
	runner := &Runner{deps: Dependencies{
		Planner:  StaticPlanner{deferredStep{}},
		Notifier: notifier,
	}}

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

// TestNotificationOutputURIPointsAtJobPathForScriptOnlyJobs pins that a script-only job's
// notification points at the same jobs path as every other command. Drafts used to be written
// under a separate prefix, so the notification had to special-case them; the storage is unified
// now and the special case is gone.
func TestNotificationOutputURIPointsAtJobPathForScriptOnlyJobs(t *testing.T) {
	task := &domain.Task{JobID: "recipe-1", Command: domain.CommandVideoRecipeDraft}
	result := &runResult{outputPath: "gs://ap-mv/veo/jobs/recipe-1/"}

	req := notificationRequest(task, result)

	if want := "gs://ap-mv/veo/jobs/recipe-1/"; req.OutputURI != want {
		t.Errorf("OutputURI = %q, want %q", req.OutputURI, want)
	}
}

// TestNotificationOutputURIKeepsJobPathForOtherCommands pins the unchanged behaviour elsewhere.
func TestNotificationOutputURIKeepsJobPathForOtherCommands(t *testing.T) {
	task := &domain.Task{JobID: "video-recipe-1", Command: domain.CommandVideoRecipeCreate}
	result := &runResult{outputPath: "gs://ap-mv/veo/jobs/video-recipe-1/"}

	req := notificationRequest(task, result)

	if req.OutputURI != "gs://ap-mv/veo/jobs/video-recipe-1/" {
		t.Errorf("OutputURI = %q, want the job output path", req.OutputURI)
	}
}
