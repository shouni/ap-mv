package pipeline

import (
	"context"
	"errors"
	"strings"
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
		Planner:          StaticPlanner{noopFilter{}},
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
		Planner:  StaticPlanner{errorFilter{}},
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
		Planner:  StaticPlanner{errorFilter{}},
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
		Planner:  StaticPlanner{deferredFilter{}},
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
