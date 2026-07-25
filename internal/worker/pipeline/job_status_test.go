package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/worker/filter"
)

// fakeJobStatusStore はメモリ上でジョブ状態を保持するテスト用ストアです。
type fakeJobStatusStore struct {
	mu       sync.Mutex
	statuses map[string]domain.JobStatus
	saved    []domain.JobStatus
}

func newFakeJobStatusStore() *fakeJobStatusStore {
	return &fakeJobStatusStore{statuses: map[string]domain.JobStatus{}}
}

func (s *fakeJobStatusStore) Save(_ context.Context, status domain.JobStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.statuses[status.JobID] = status
	s.saved = append(s.saved, status)
	return nil
}

func (s *fakeJobStatusStore) Get(_ context.Context, jobID string) (*domain.JobStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	status, ok := s.statuses[jobID]
	if !ok {
		return nil, errors.New("not found")
	}
	return &status, nil
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

func statusRunner(store *fakeJobStatusStore, flt filter.Filter) *Runner {
	return &Runner{deps: Dependencies{
		Planner:          StaticPlanner{flt},
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
	runner := statusRunner(store, noopFilter{})

	if err := runner.Execute(context.Background(), videoTask()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	equalStates(t, store.states(), domain.JobStateRunning, domain.JobStateSucceeded)

	final := store.statuses["mv-20260726-123456-abcdef123456"]
	if final.Attempts != 1 {
		t.Fatalf("Attempts = %d, want 1", final.Attempts)
	}
	if final.OutputURI == "" {
		t.Fatal("成功時は成果物の保存先を記録すること")
	}
}

func TestExecuteRecordsFailureWithReason(t *testing.T) {
	store := newFakeJobStatusStore()
	runner := statusRunner(store, errorFilter{})

	if err := runner.Execute(context.Background(), videoTask()); err == nil {
		t.Fatal("Execute() error = nil, want error")
	}

	equalStates(t, store.states(), domain.JobStateRunning, domain.JobStateFailed)

	final := store.statuses["mv-20260726-123456-abcdef123456"]
	if final.Error == "" {
		t.Fatal("失敗理由が記録されていない")
	}
}

// カット分割された動画生成の途中（deferred）を succeeded にしてはいけない。
// succeeded にすると、同じ job_id を引き継ぐ継続タスクが再実行ガードで
// 打ち切られ、残りのカットが永久に生成されなくなる。
func TestExecuteKeepsRunningWhenDeferred(t *testing.T) {
	store := newFakeJobStatusStore()
	runner := statusRunner(store, deferredFilter{})

	if err := runner.Execute(context.Background(), videoTask()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	equalStates(t, store.states(), domain.JobStateRunning)

	final := store.statuses["mv-20260726-123456-abcdef123456"]
	if final.State != domain.JobStateRunning {
		t.Fatalf("State = %q, want running", final.State)
	}
	if final.IsTerminal() {
		t.Fatal("deferred なジョブが終了扱いになっている")
	}
}

// 継続タスクは同じ job_id で処理を引き継げること（ガードが途中で発動しない）。
func TestExecuteAllowsContinuationOfDeferredJob(t *testing.T) {
	store := newFakeJobStatusStore()
	task := videoTask()

	if err := statusRunner(store, deferredFilter{}).Execute(context.Background(), task); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// 継続タスクは同じ job_id を引き継ぎ、コマンドと引き継ぎレシピだけが変わる。
	continuation := task
	continuation.Command = domain.CommandVideoGenContinuation
	continuation.RecipeURL = "gs://bucket/ap-mv/veo/jobs/mv-20260726-123456-abcdef123456/video_music_meta.json"
	if err := statusRunner(store, noopFilter{}).Execute(context.Background(), continuation); err != nil {
		t.Fatalf("継続タスクの Execute() error = %v", err)
	}

	final := store.statuses["mv-20260726-123456-abcdef123456"]
	if final.State != domain.JobStateSucceeded {
		t.Fatalf("State = %q, want succeeded", final.State)
	}
	if final.Attempts != 2 {
		t.Fatalf("Attempts = %d, want 2 (試行回数が引き継がれていない)", final.Attempts)
	}
}

// Cloud Tasks の再配信で、完了済みジョブの生成をやり直さないこと。
func TestExecuteSkipsAlreadySucceededJob(t *testing.T) {
	store := newFakeJobStatusStore()
	counter := &countingFilter{}
	runner := statusRunner(store, counter)

	task := videoTask()
	if err := runner.Execute(context.Background(), task); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if err := runner.Execute(context.Background(), task); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if counter.calls != 1 {
		t.Fatalf("filter calls = %d, want 1 (完了済みジョブが再実行されている)", counter.calls)
	}
}

// 失敗したジョブは再配信で作り直せること（ガードが効きすぎないこと）。
func TestExecuteRetriesFailedJob(t *testing.T) {
	store := newFakeJobStatusStore()

	if err := statusRunner(store, errorFilter{}).Execute(context.Background(), videoTask()); err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if err := statusRunner(store, noopFilter{}).Execute(context.Background(), videoTask()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	final := store.statuses["mv-20260726-123456-abcdef123456"]
	if final.State != domain.JobStateSucceeded {
		t.Fatalf("State = %q, want succeeded", final.State)
	}
}

// ストア未設定でもパイプラインは通常どおり動作すること。
func TestExecuteWorksWithoutStatusStore(t *testing.T) {
	counter := &countingFilter{}
	runner := &Runner{deps: Dependencies{
		Planner:          StaticPlanner{counter},
		WorkflowResolver: StaticWorkflowResolver{Workflows: &orchestrator.Workflows{}},
	}}

	if err := runner.Execute(context.Background(), videoTask()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if counter.calls != 1 {
		t.Fatalf("filter calls = %d, want 1", counter.calls)
	}
}

// 生成が固まってもタスクは打ち切られ、failed として記録されること。
func TestExecuteTimesOutAndRecordsFailure(t *testing.T) {
	store := newFakeJobStatusStore()
	runner := &Runner{deps: Dependencies{
		Planner:          StaticPlanner{blockingFilter{}},
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
	// 打ち切られたタスクは Cloud Tasks の再試行で作り直せる必要がある。
	if final.IsTerminal() {
		t.Fatal("タイムアウトしたジョブが終了扱いになっている")
	}
}

// countingFilter は実行回数を数えるフィルターです。
type countingFilter struct {
	calls int
}

func (countingFilter) Name() string { return "counting" }

func (f *countingFilter) Execute(context.Context, *filter.Context) error {
	f.calls++
	return nil
}

// blockingFilter はコンテキストがキャンセルされるまで応答しないフィルターです。
type blockingFilter struct{}

func (blockingFilter) Name() string { return "blocking" }

func (blockingFilter) Execute(ctx context.Context, _ *filter.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
