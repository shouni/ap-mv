package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shouni/gcp-kit/jobstatus"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/domain"
)

// orderLog は、状態の記録と投入がどの順で呼ばれたかを 1 本の列に残します。
type orderLog struct{ events []string }

// orderedStatusStore は JobStatusStore のフェイクで、Save / Delete を orderLog へ流します。
type orderedStatusStore struct{ log *orderLog }

func (s orderedStatusStore) Save(_ context.Context, _ string, _ domain.JobStatus) error {
	s.log.events = append(s.log.events, "save")
	return nil
}

func (s orderedStatusStore) Get(context.Context, string) (domain.JobStatus, error) {
	return domain.JobStatus{}, jobstatus.ErrNotFound
}

func (s orderedStatusStore) Delete(context.Context, string) error {
	s.log.events = append(s.log.events, "delete")
	return nil
}

func (s orderedStatusStore) List(context.Context, int, int, ...jobstatus.ListOption) ([]domain.JobStatus, domain.PageMeta, error) {
	return nil, domain.PageMeta{}, nil
}

// orderedQueue は TaskQueue のフェイクで、投入を orderLog へ流し、err があれば失敗します。
type orderedQueue struct {
	log *orderLog
	err error
}

func (q orderedQueue) Enqueue(context.Context, *domain.Task) error {
	q.log.events = append(q.log.events, "enqueue")
	return q.err
}

func (q orderedQueue) EnqueueWithName(_ context.Context, _ string, task *domain.Task) error {
	return q.Enqueue(context.Background(), task)
}

func newOrderTestHandler(t *testing.T, log *orderLog, queueErr error) *Handler {
	t.Helper()
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.JobStatus = orderedStatusStore{log: log}
	h.Queue = orderedQueue{log: log, err: queueErr}
	return h
}

func orderTestTask() *domain.Task {
	return &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeDraft, Text: "歌詞"}
}

// TestEnqueueRecordsQueuedBeforeEnqueue は、queued の記録が投入より先であることを固定します。
//
// Cloud Tasks は数十ミリ秒で届くため、逆順だと Worker が書いた running を遅れてきた
// queued が上書きし、実行中のジョブが履歴では受付済みのまま止まって見えます
// （public-docs のワーカー規約 2.1）。
func TestEnqueueRecordsQueuedBeforeEnqueue(t *testing.T) {
	log := &orderLog{}
	h := newOrderTestHandler(t, log, nil)

	req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(""))
	rec := httptest.NewRecorder()
	h.enqueue(rec, req, orderTestTask())

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if got := strings.Join(log.events, ">"); got != "save>enqueue" {
		t.Errorf("order = %s, want save>enqueue", got)
	}
}

// TestEnqueueDiscardsQueuedWhenEnqueueFails は、投入に失敗したジョブが履歴に残らないことを
// 固定します。queued の記録を先に書く以上、積めなかったときは取り消さないと
// 誰も処理しない行が一覧に並びます。
func TestEnqueueDiscardsQueuedWhenEnqueueFails(t *testing.T) {
	log := &orderLog{}
	h := newOrderTestHandler(t, log, errors.New("queue unavailable"))

	req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(""))
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	h.enqueue(rec, req, orderTestTask())

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	if got := strings.Join(log.events, ">"); got != "save>enqueue>delete" {
		t.Errorf("order = %s, want save>enqueue>delete", got)
	}
}
