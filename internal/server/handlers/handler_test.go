package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"ap-mv/internal/domain"
)

type recordingQueue struct {
	task *domain.Task
}

func (q *recordingQueue) Enqueue(_ context.Context, task *domain.Task) error {
	q.task = task
	return nil
}

func TestPostComposeSupportsKeyframeRunMode(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandler(os.DirFS("../../.."), queue)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	form := url.Values{
		"csrf_token": {"token"},
		"text":       {"source text"},
		"run_mode":   {"keyframe"},
	}
	req := httptest.NewRequest(http.MethodPost, "/web/compose", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostCompose(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("PostCompose status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if queue.task == nil {
		t.Fatal("queued task is nil")
	}
	if queue.task.Command != domain.CommandComposeToKeyframe {
		t.Fatalf("queued command = %q, want %q", queue.task.Command, domain.CommandComposeToKeyframe)
	}
}

func TestPostComposeDefaultsToFullCompose(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandler(os.DirFS("../../.."), queue)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	form := url.Values{
		"csrf_token": {"token"},
		"text":       {"source text"},
	}
	req := httptest.NewRequest(http.MethodPost, "/web/compose", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostCompose(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("PostCompose status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if queue.task == nil {
		t.Fatal("queued task is nil")
	}
	if queue.task.Command != domain.CommandCompose {
		t.Fatalf("queued command = %q, want %q", queue.task.Command, domain.CommandCompose)
	}
}
