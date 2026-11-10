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

// orderLog keeps, in one sequence, the order in which status records and enqueues were called.
type orderLog struct{ events []string }

// orderedStatusStore is a JobStatusStore fake that feeds Save / Delete into orderLog.
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

// orderedQueue is a TaskQueue fake that feeds enqueues into orderLog and fails when err is set.
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

// TestEnqueueRecordsQueuedBeforeEnqueue pins that the queued record comes before the enqueue.
//
// Cloud Tasks delivers within tens of milliseconds, so in the reverse order a late queued
// overwrites the running the worker wrote, and a running job looks stuck at accepted in history
// (section 2.1 of the worker conventions in public-docs).
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

// TestEnqueueDiscardsQueuedWhenEnqueueFails pins that a job that failed to enqueue does not stay
// in history. Since the queued record is written first, it must be withdrawn when the task could
// not be queued, or rows nobody will process line up in the listing.
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
