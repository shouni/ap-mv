package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/domain"
)

func newJobPromptHandler(t *testing.T, repo fakeHistoryRepository) *Handler {
	t.Helper()
	h, err := NewHandlerWithOptions(assets.Templates, nil, ModelOptions{}, CharacterOptions{})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions() error = %v", err)
	}
	h.HistoryRepository = repo
	return h
}

func jobPromptRequest(target, accept string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "job-1")
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
}

func jobPromptRecipe() *domain.VideoRecipe {
	return &domain.VideoRecipe{
		AspectRatio: "9:16",
		Cuts: []domain.VideoCut{
			{CutIndex: 1, CharacterID: "tsumugi", VisualAnchor: "opening shot", AudioCue: "soft intro", DurationSec: 15, StartSec: 0, EndSec: 15},
			{CutIndex: 2, CharacterID: "tsumugi", VisualAnchor: "second shot", DurationSec: 8, StartSec: 15, EndSec: 23},
		},
	}
}

func TestJobPromptDefaultsToPlainText(t *testing.T) {
	h := newJobPromptHandler(t, fakeHistoryRepository{recipe: jobPromptRecipe()})
	rec := httptest.NewRecorder()

	h.JobPrompt(rec, jobPromptRequest("/jobs/job-1/prompt", ""))

	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	body := rec.Body.String()
	for _, want := range []string{"Create a music video for the attached song", "[0:00 - 0:15]: opening shot", "  Music: soft intro"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	// 貼り先は Flow Music。Veo のモード別ガイダンスは、そこに無い入力を指すので入れない。
	if strings.Contains(body, "starting keyframe image") {
		t.Errorf("the Flow Music brief must not carry Veo's per-mode guidance:\n%s", body)
	}
}

// TestJobPromptJSONNamesTheReferenceArt verifies the JSON carries the same text plus the
// character art to attach, picked for the recipe's aspect ratio and listed once per character.
func TestJobPromptJSONNamesTheReferenceArt(t *testing.T) {
	characters, err := characterkit.NewCharacters([]characterkit.Character{{
		ID: "tsumugi", Name: "Tsumugi", VisualCues: []string{"orange hair"}, IsDefault: true,
		ReferenceURL:  "gs://b/default.png",
		ReferenceURLs: map[string]string{"9:16": "gs://b/9x16.png"},
	}})
	if err != nil {
		t.Fatalf("NewCharacters() error = %v", err)
	}
	h := newJobPromptHandler(t, fakeHistoryRepository{recipe: jobPromptRecipe()})
	h.Characters = characters
	rec := httptest.NewRecorder()

	h.JobPrompt(rec, jobPromptRequest("/jobs/job-1/prompt", "application/json"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got jobPromptResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.JobID != "job-1" || !strings.Contains(got.Prompt, "[0:15 - 0:23]: second shot") {
		t.Errorf("response = %+v", got)
	}
	want := []promptReferenceImage{{CharacterID: "tsumugi", Name: "Tsumugi", URI: "gs://b/9x16.png"}}
	if len(got.ReferenceImages) != 1 || got.ReferenceImages[0] != want[0] {
		t.Errorf("reference_images = %+v, want %+v", got.ReferenceImages, want)
	}
}

// TestHistoryDetailOffersTheFlowPromptPanel verifies the detail page carries the panel and its
// script. The text itself is fetched when the panel opens, so the page only needs the URL.
func TestHistoryDetailOffersTheFlowPromptPanel(t *testing.T) {
	h := newJobPromptHandler(t, fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: "job-1", Title: "MV", CutCount: 1},
			Cuts:         []domain.VideoHistoryCut{{CutIndex: 1, DurationSec: 8}},
		},
	})
	rec := httptest.NewRecorder()

	h.Job(rec, jobPromptRequest("/jobs/job-1", ""))

	for _, want := range []string{`data-prompt-url="/jobs/job-1/prompt"`, `src="/static/js/flow_prompt.js"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("detail page missing %s", want)
		}
	}
}
