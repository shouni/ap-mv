package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/domain"
)

// Scripts live outside the templates and only the pages that need them load them.
// Left inline, the page's structure and behaviour were mixed in one file, and template values
// were embedded into JS string literals.
func TestPagesLoadTheirScripts(t *testing.T) {
	t.Parallel()

	body := renderHistoryDetailPage(t)
	if strings.Contains(body, "<script>") {
		t.Error("an inline script remains")
	}
	for _, attribute := range []string{"onclick=", "onsubmit=", "onchange="} {
		if strings.Contains(body, attribute) {
			t.Errorf("a %s attribute remains", attribute)
		}
	}
	// Delete is sent via fetch, so it needs a CSRF token readable from outside the form.
	if !strings.Contains(body, `id="csrf_token"`) {
		t.Error("no hidden input for the CSRF token")
	}
	if !strings.Contains(body, `data-delete-url="/jobs/`) {
		t.Error("the delete button has no data-delete-url")
	}
}

// The nav's model names come from the configuration. When they were fixed text, swapping models
// left the display pointing at the old model.
func TestNavShowsConfiguredGeminiModel(t *testing.T) {
	t.Parallel()

	if body := renderHistoryPage(t); !strings.Contains(body, "gemini-test-model") {
		t.Error("the configured Gemini model is not in the nav")
	} else if strings.Contains(body, "Gemini 3 Flash") {
		t.Error("a fixed model name remains")
	}
}

// renderHistoryPage renders the history listing as the representative page without a form.
// Even pages that do not build model options need the model names in the nav.
func renderHistoryPage(t *testing.T) string {
	t.Helper()

	h, err := NewHandlerWithOptions(assets.Templates, nil,
		ModelOptions{
			GeminiModels:       []string{"gemini-test-model"},
			DefaultGeminiModel: "gemini-test-model",
		},
		CharacterOptions{},
	)
	if err != nil {
		t.Fatalf("NewHandlerWithOptions() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		page: domain.VideoHistoryPage{Items: []domain.VideoHistory{{
			JobID: "recipe-20260618-081931-abcdef123456",
			Title: "スクリプト外出しの確認",
		}}},
	}

	rec := httptest.NewRecorder()
	h.JobList(rec, httptest.NewRequest(http.MethodGet, "/jobs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("History status = %d; body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// renderHistoryDetailPage renders the history detail as the representative page with a delete
// button and a CSRF token. Deleting from the listing was a drafts-only path, so after the merge the
// detail page is what this checks.
func renderHistoryDetailPage(t *testing.T) string {
	t.Helper()

	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	const jobID = "recipe-20260618-081931-abcdef123456"
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: jobID, Title: "削除導線の確認"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/jobs/"+jobID, nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.Job(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d; body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestNavMarksCurrentPage checks that the nav marks the current place with active + aria-current.
// The sibling apps (the music-generation service / ap-story / ap-voice) all have it and only ap-mv
// lacked it, so the nav did not tell which page you were on.
func TestNavMarksCurrentPage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		target   string
		wantHref string
	}{
		"history":             {target: "/jobs", wantHref: `href="/jobs"`},
		"script-only listing": {target: "/jobs?stage=script", wantHref: `href="/jobs?stage=script"`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h, err := NewHandler(assets.Templates, nil)
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}
			h.HistoryRepository = fakeHistoryRepository{}

			rec := httptest.NewRecorder()
			h.JobList(rec, httptest.NewRequest(http.MethodGet, tt.target, nil))

			body := rec.Body.String()
			if !strings.Contains(body, `aria-current="page" `+tt.wantHref) {
				t.Errorf("%s should be marked as the current page; body=%s", tt.wantHref, body)
			}
			// Only one active. If both were current, you could not tell where you are.
			if got := strings.Count(body, `aria-current="page"`); got != 1 {
				t.Errorf("aria-current count = %d, want exactly 1", got)
			}
		})
	}
}

// TestNavLinksCarryIcons pins that every nav item has a Bootstrap icon, matching ap-story and
// ap-voice. The stylesheet was already loaded but only the brand and footer used it.
func TestNavLinksCarryIcons(t *testing.T) {
	t.Parallel()

	body := renderHistoryPage(t)
	for _, icon := range []string{"bi-house", "bi-film", "bi-file-earmark-text", "bi-clock-history"} {
		if !strings.Contains(body, icon) {
			t.Errorf("nav is missing the %s icon", icon)
		}
	}
}
