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

// 本物の GCS クライアントと同じく context を尊重します。ここを無視すると、
// 打ち切られたジョブの終端記録がキャンセル済み context で行われていてもテストが
// 通ってしまい、状態が running のまま固着するバグを見逃します。
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

// List は一覧のためのクエリで、パイプラインは呼びません。ports を満たすためだけの実装です。
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
		// 未記録は ErrNotFound を包んで返す（Store の契約）。素のエラーを返すと
		// 「読めなかった」に分類され、再実行ガードがエラーを返します。
		return domain.JobStatus{}, fmt.Errorf("%w: 未記録", domain.ErrJobStatusNotFound)
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

// 期限（PIPELINE_TIMEOUT）の発火とほぼ同時に生成が完了したジョブも、succeeded として
// 記録され通知されること。
//
// 締切はフィルターの実行にだけ被せる必要があります。ctx 全体を締切で上書きすると、
// 期限と前後して完了したジョブの成功記録が DeadlineExceeded で失敗し、動画は GCS に
// あるのに状態は running のまま固着します。mv-queue は max_attempts = 1 なので
// 再試行も来ず、手で直すしかありません。
func TestExecuteRecordsSuccessCompletedAtDeadline(t *testing.T) {
	store := newFakeJobStatusStore()
	notifier := &recordingNotifier{}
	runner := &Runner{deps: Dependencies{
		Planner:          StaticPlanner{deadlineFilter{}},
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
		t.Fatalf("completion notifications = %d, want 1 (期限際の完了が通知されていない)", len(notifier.completed))
	}
}

// 呼び出し元の切断（Cloud Tasks の dispatch deadline 超過）とほぼ同時に生成が完了した
// ジョブも、succeeded として記録され通知されること。PIPELINE_TIMEOUT と違い、こちらは
// パイプラインが上限を設けていなくても起きます。
func TestExecuteRecordsSuccessWhenCallerCancelsAtCompletion(t *testing.T) {
	store := newFakeJobStatusStore()
	notifier := &recordingNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &Runner{deps: Dependencies{
		Planner:          StaticPlanner{cancelFilter{cancel: cancel}},
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
		t.Fatalf("completion notifications = %d, want 1 (切断と同時の完了が通知されていない)", len(notifier.completed))
	}
}

// deadlineFilter は context の期限まで待ってから成功します。
// PIPELINE_TIMEOUT の発火とほぼ同時に生成が完了した状況を再現します。
type deadlineFilter struct{}

func (deadlineFilter) Name() string { return "deadline" }

func (deadlineFilter) Execute(ctx context.Context, _ *filter.Context) error {
	<-ctx.Done()
	return nil
}

// cancelFilter は成功を返す直前に cancel を呼びます。呼び出し元 context の切断
// （Cloud Tasks の dispatch deadline 超過）と同時に生成が完了した状況を再現します。
type cancelFilter struct{ cancel context.CancelFunc }

func (cancelFilter) Name() string { return "cancel" }

func (f cancelFilter) Execute(context.Context, *filter.Context) error {
	f.cancel()
	return nil
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
