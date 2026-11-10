package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/shouni/gcp-kit/jobstatus"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/pipeline/step"
)

// fakeJobStatusStore is a test store holding job status in memory.
type fakeJobStatusStore struct {
	mu       sync.Mutex
	statuses map[string]domain.JobStatus
	saved    []domain.JobStatus
}

func newFakeJobStatusStore() *fakeJobStatusStore {
	return &fakeJobStatusStore{statuses: map[string]domain.JobStatus{}}
}

// It respects the context, as the real GCS client does. Ignoring it would let tests pass even
// when a cut-off job's terminal record is made on a cancelled context, missing the bug where the
// state sticks at running.
func (s *fakeJobStatusStore) Save(ctx context.Context, _ string, status domain.JobStatus) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.statuses[status.JobID] = status
	s.saved = append(s.saved, status)
	return nil
}

// List is the listing query, which the pipeline never calls. It exists only to satisfy ports.
func (s *fakeJobStatusStore) List(context.Context, int, int, ...jobstatus.ListOption) ([]domain.JobStatus, domain.PageMeta, error) {
	return nil, domain.PageMeta{}, nil
}

func (s *fakeJobStatusStore) Delete(ctx context.Context, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.statuses, jobID)
	return nil
}

func (s *fakeJobStatusStore) Get(ctx context.Context, jobID string) (domain.JobStatus, error) {
	if err := ctx.Err(); err != nil {
		return domain.JobStatus{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	status, ok := s.statuses[jobID]
	if !ok {
		// Not recorded is returned wrapping ErrNotFound (the Store contract). Returning a bare error
		// would be classified as "could not read" and the re-run guard would return an error.
		return domain.JobStatus{}, fmt.Errorf("%w: not recorded", domain.ErrJobStatusNotFound)
	}
	return status, nil
}

func (s *fakeJobStatusStore) states() []domain.JobState {
	s.mu.Lock()
	defer s.mu.Unlock()

	states := make([]domain.JobState, 0, len(s.saved))
	for _, status := range s.saved {
		states = append(states, status.State)
	}
	return states
}

func statusRunner(store *fakeJobStatusStore, st step.Step) *Runner {
	return &Runner{deps: Dependencies{
		Planner:          StaticPlanner{st},
		WorkflowResolver: StaticWorkflowResolver{Workflows: &orchestrator.Workflows{}},
		OutputBaseURI:    "gs://bucket/ap-mv/veo/jobs",
		JobStatus:        store,
	}}
}

func videoTask() domain.Task {
	return domain.Task{
		JobID:     "mv-20260726-123456-abcdef123456",
		Command:   domain.CommandVideoRecipeCreate,
		SourceURL: "gs://bucket/music_recipe.json",
	}
}

func equalStates(t *testing.T, got []domain.JobState, want ...domain.JobState) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("states = %v, want %v", got, want)
		}
	}
}

func TestExecuteRecordsRunningThenSucceeded(t *testing.T) {
	store := newFakeJobStatusStore()
	runner := statusRunner(store, noopStep{})

	if err := runner.Execute(context.Background(), videoTask()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	equalStates(t, store.states(), domain.JobStateRunning, domain.JobStateSucceeded)

	final := store.statuses["mv-20260726-123456-abcdef123456"]
	if final.Attempts != 1 {
		t.Fatalf("Attempts = %d, want 1", final.Attempts)
	}
	if final.OutputURI == "" {
		t.Fatal("on success, where the artifacts are stored must be recorded")
	}
}

func TestExecuteRecordsFailureWithReason(t *testing.T) {
	store := newFakeJobStatusStore()
	runner := statusRunner(store, errorStep{})

	if err := runner.Execute(context.Background(), videoTask()); err == nil {
		t.Fatal("Execute() error = nil, want error")
	}

	equalStates(t, store.states(), domain.JobStateRunning, domain.JobStateFailed)

	final := store.statuses["mv-20260726-123456-abcdef123456"]
	if final.Error == "" {
		t.Fatal("the failure reason was not recorded")
	}
}

// A cut-split video generation in progress (deferred) must not be marked succeeded.
// If it were, the continuation task inheriting the same job_id would be stopped by the re-run
// guard and the remaining cuts would never be generated.
func TestExecuteKeepsRunningWhenDeferred(t *testing.T) {
	store := newFakeJobStatusStore()
	runner := statusRunner(store, deferredStep{})

	if err := runner.Execute(context.Background(), videoTask()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	equalStates(t, store.states(), domain.JobStateRunning)

	final := store.statuses["mv-20260726-123456-abcdef123456"]
	if final.State != domain.JobStateRunning {
		t.Fatalf("State = %q, want running", final.State)
	}
	if final.IsTerminal() {
		t.Fatal("a deferred job is treated as finished")
	}
}

// A continuation task can carry on under the same job_id (the guard does not fire midway).
func TestExecuteAllowsContinuationOfDeferredJob(t *testing.T) {
	store := newFakeJobStatusStore()
	task := videoTask()

	if err := statusRunner(store, deferredStep{}).Execute(context.Background(), task); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// A continuation task inherits the same job_id; only the command and the carried recipe change.
	continuation := task
	continuation.Command = domain.CommandVideoGenContinuation
	continuation.RecipeURL = "gs://bucket/ap-mv/veo/jobs/mv-20260726-123456-abcdef123456/video_music_meta.json"
	if err := statusRunner(store, noopStep{}).Execute(context.Background(), continuation); err != nil {
		t.Fatalf("continuation task Execute() error = %v", err)
	}

	final := store.statuses["mv-20260726-123456-abcdef123456"]
	if final.State != domain.JobStateSucceeded {
		t.Fatalf("State = %q, want succeeded", final.State)
	}
	if final.Attempts != 2 {
		t.Fatalf("Attempts = %d, want 2 (the attempt count was not carried over)", final.Attempts)
	}
}

// A Cloud Tasks redelivery does not redo a finished job's generation.
func TestExecuteSkipsAlreadySucceededJob(t *testing.T) {
	store := newFakeJobStatusStore()
	counter := &countingStep{}
	runner := statusRunner(store, counter)

	task := videoTask()
	if err := runner.Execute(context.Background(), task); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if err := runner.Execute(context.Background(), task); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if counter.calls != 1 {
		t.Fatalf("step calls = %d, want 1 (a finished job was re-run)", counter.calls)
	}
}

// A failed job can be remade on redelivery (the guard does not overreach).
func TestExecuteRetriesFailedJob(t *testing.T) {
	store := newFakeJobStatusStore()

	if err := statusRunner(store, errorStep{}).Execute(context.Background(), videoTask()); err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if err := statusRunner(store, noopStep{}).Execute(context.Background(), videoTask()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	final := store.statuses["mv-20260726-123456-abcdef123456"]
	if final.State != domain.JobStateSucceeded {
		t.Fatalf("State = %q, want succeeded", final.State)
	}
}

// The pipeline works as usual even with no store configured.
func TestExecuteWorksWithoutStatusStore(t *testing.T) {
	counter := &countingStep{}
	runner := &Runner{deps: Dependencies{
		Planner:          StaticPlanner{counter},
		WorkflowResolver: StaticWorkflowResolver{Workflows: &orchestrator.Workflows{}},
	}}

	if err := runner.Execute(context.Background(), videoTask()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if counter.calls != 1 {
		t.Fatalf("step calls = %d, want 1", counter.calls)
	}
}

// Even if generation hangs, the task is cut off and recorded as failed.
func TestExecuteTimesOutAndRecordsFailure(t *testing.T) {
	store := newFakeJobStatusStore()
	runner := &Runner{deps: Dependencies{
		Planner:          StaticPlanner{blockingStep{}},
		WorkflowResolver: StaticWorkflowResolver{Workflows: &orchestrator.Workflows{}},
		JobStatus:        store,
		Timeout:          50 * time.Millisecond,
	}}

	err := runner.Execute(context.Background(), videoTask())
	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Execute() error = %v, want context.DeadlineExceeded", err)
	}

	final := store.statuses["mv-20260726-123456-abcdef123456"]
	if final.State != domain.JobStateFailed {
		t.Fatalf("State = %q, want failed", final.State)
	}
	// A cut-off task must be remakeable by a Cloud Tasks retry.
	if final.IsTerminal() {
		t.Fatal("a timed-out job is treated as finished")
	}
}

// A job whose generation finishes almost exactly when the deadline (PIPELINE_TIMEOUT) fires is
// still recorded as succeeded and notified.
//
// The deadline must wrap only the step run. Overriding the whole ctx with the deadline would make
// the success record of a job finishing around the deadline fail with DeadlineExceeded, and the
// state would stick at running although the video is in GCS. mv-queue has max_attempts = 1, so no
// retry comes either and it can only be fixed by hand.
func TestExecuteRecordsSuccessCompletedAtDeadline(t *testing.T) {
	store := newFakeJobStatusStore()
	notifier := &recordingNotifier{}
	runner := &Runner{deps: Dependencies{
		Planner:          StaticPlanner{deadlineStep{}},
		WorkflowResolver: StaticWorkflowResolver{Workflows: &orchestrator.Workflows{}},
		OutputBaseURI:    "gs://bucket/ap-mv/veo/jobs",
		JobStatus:        store,
		Notifier:         notifier,
		Timeout:          50 * time.Millisecond,
	}}

	if err := runner.Execute(context.Background(), videoTask()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	equalStates(t, store.states(), domain.JobStateRunning, domain.JobStateSucceeded)
	if len(notifier.completed) != 1 {
		t.Fatalf("completion notifications = %d, want 1 (completion at the deadline was not notified)", len(notifier.completed))
	}
}

// A job whose generation finishes almost exactly when the caller disconnects (Cloud Tasks' dispatch
// deadline exceeded) is still recorded as succeeded and notified. Unlike PIPELINE_TIMEOUT, this
// happens even when the pipeline sets no limit.
func TestExecuteRecordsSuccessWhenCallerCancelsAtCompletion(t *testing.T) {
	store := newFakeJobStatusStore()
	notifier := &recordingNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &Runner{deps: Dependencies{
		Planner:          StaticPlanner{cancelStep{cancel: cancel}},
		WorkflowResolver: StaticWorkflowResolver{Workflows: &orchestrator.Workflows{}},
		OutputBaseURI:    "gs://bucket/ap-mv/veo/jobs",
		JobStatus:        store,
		Notifier:         notifier,
	}}

	if err := runner.Execute(ctx, videoTask()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	equalStates(t, store.states(), domain.JobStateRunning, domain.JobStateSucceeded)
	if len(notifier.completed) != 1 {
		t.Fatalf("completion notifications = %d, want 1 (completion at the disconnect was not notified)", len(notifier.completed))
	}
}

// deadlineStep waits until the context's deadline and then succeeds.
// It reproduces generation finishing almost exactly when PIPELINE_TIMEOUT fires.
type deadlineStep struct{}

func (deadlineStep) Name() string { return "deadline" }

func (deadlineStep) Execute(ctx context.Context, _ *step.Context) error {
	<-ctx.Done()
	return nil
}

// cancelStep calls cancel just before returning success. It reproduces generation finishing at
// the same time as the caller's context is cut (Cloud Tasks' dispatch deadline exceeded).
type cancelStep struct{ cancel context.CancelFunc }

func (cancelStep) Name() string { return "cancel" }

func (f cancelStep) Execute(context.Context, *step.Context) error {
	f.cancel()
	return nil
}

// countingStep is a step that counts its runs.
type countingStep struct {
	calls int
}

func (countingStep) Name() string { return "counting" }

func (f *countingStep) Execute(context.Context, *step.Context) error {
	f.calls++
	return nil
}

// blockingStep is a step that does not respond until the context is cancelled.
type blockingStep struct{}

func (blockingStep) Name() string { return "blocking" }

func (blockingStep) Execute(ctx context.Context, _ *step.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
