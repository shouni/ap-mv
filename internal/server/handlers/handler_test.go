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
	h, err := NewHandler(os.DirFS("../../.."), queue, ModelOptions{
		GeminiModels:       []string{"gemini-default", "gemini-alt"},
		ImageModels:        []string{"image-default", "image-alt"},
		DefaultGeminiModel: "gemini-default",
		DefaultImageModel:  "image-default",
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	form := url.Values{
		"csrf_token":  {"token"},
		"text":        {"source text"},
		"run_mode":    {"keyframe"},
		"text_model":  {"gemini-alt"},
		"image_model": {"image-alt"},
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
	if queue.task.TextModel != "gemini-alt" {
		t.Fatalf("queued text model = %q, want gemini-alt", queue.task.TextModel)
	}
	if queue.task.ImageModel != "image-alt" {
		t.Fatalf("queued image model = %q, want image-alt", queue.task.ImageModel)
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

func TestComposeFormRendersModelSelects(t *testing.T) {
	h, err := NewHandler(os.DirFS("../../.."), nil, ModelOptions{
		GeminiModels:       []string{"gemini-default", "gemini-alt"},
		ImageModels:        []string{"image-default", "image-alt"},
		DefaultGeminiModel: "gemini-default",
		DefaultImageModel:  "image-default",
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/web/compose", nil)
	req = req.WithContext(WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.ComposeForm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ComposeForm status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`name="text_model"`,
		`value="gemini-default" selected`,
		`value="gemini-alt"`,
		`name="image_model"`,
		`value="image-default" selected`,
		`value="image-alt"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("ComposeForm body missing %q: %s", want, body)
		}
	}
}
