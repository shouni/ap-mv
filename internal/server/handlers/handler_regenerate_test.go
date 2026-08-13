package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/gcp-kit/auth"
)

// TestPostRegenerateCutKeyframeInheritsHistoryAspectRatio verifies the queued task's aspect
// ratio always comes from the existing recipe (history.AspectRatio), never from user input —
// there is no aspect_ratio form field for cut regeneration, since a single cut can't have a
// different aspect ratio from the rest of its job.
func TestPostRegenerateCutKeyframeInheritsHistoryAspectRatio(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandler(assets.Templates, queue)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{
				JobID:       "job-1",
				StorageURI:  "gs://bucket/jobs/job-1/video_music_meta.json",
				AspectRatio: "9:16",
			},
			Cuts: []domain.VideoHistoryCut{{CutIndex: 1, VisualAnchor: "original anchor"}},
		},
	}

	form := url.Values{"csrf_token": {"token"}}
	req := newRegenerateRequest(http.MethodPost, "/web/history/job-1/cuts/1/regenerate-keyframe", "job-1", "1", strings.NewReader(form.Encode()))
	req = req.WithContext(auth.WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostRegenerateCutKeyframe(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("PostRegenerateCutKeyframe status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if queue.task == nil {
		t.Fatal("queued task is nil")
	}
	if queue.task.VeoAspectRatio != "9:16" {
		t.Fatalf("queued VeoAspectRatio = %q, want %q (inherited from history)", queue.task.VeoAspectRatio, "9:16")
	}
}

// TestPostGenerateVideoFromHistoryInheritsAspectRatio verifies the queued task's aspect ratio
// comes from the existing recipe (history.AspectRatio), ignoring any aspect_ratio form value a
// caller might still send (the generate-video form no longer offers this choice).
func TestPostGenerateVideoFromHistoryInheritsAspectRatio(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandler(assets.Templates, queue)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{
				JobID:       "job-1",
				StorageURI:  "gs://bucket/jobs/job-1/video_music_meta.json",
				AspectRatio: "9:16",
			},
		},
	}

	form := url.Values{
		"csrf_token":   {"token"},
		"target":       {"full"},
		"aspect_ratio": {"16:9"}, // must be ignored even if a stale client still sends it
	}
	req := httptest.NewRequest(http.MethodPost, "/web/history/job-1/generate-video", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	req = req.WithContext(auth.WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostGenerateVideoFromHistory(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("PostGenerateVideoFromHistory status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if queue.task == nil {
		t.Fatal("queued task is nil")
	}
	if queue.task.VeoAspectRatio != "9:16" {
		t.Fatalf("queued VeoAspectRatio = %q, want %q (inherited from history, not form)", queue.task.VeoAspectRatio, "9:16")
	}
}

// newRegenerateSectionRequest builds a request carrying jobID/sectionIndex chi route params.
func newRegenerateSectionRequest(method, target, jobID, sectionIndex string, body *strings.Reader) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, target, body)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", jobID)
	routeContext.URLParams.Add("sectionIndex", sectionIndex)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
}

// sectionRegenHistory returns a two-section history whose cuts split 2/1 across the sections,
// shared by the section regenerate form and submit tests.
func sectionRegenHistory() domain.VideoHistoryDetail {
	return domain.VideoHistoryDetail{
		VideoHistory: domain.VideoHistory{JobID: "job-1", StorageURI: "gs://bucket/jobs/job-1/video_music_meta.json"},
		Sections: []domain.VideoHistorySection{
			{SectionIndex: 0, Name: "Verse", StartSeconds: 0, EndSeconds: 16},
			{SectionIndex: 1, Name: "Chorus", StartSeconds: 16, EndSeconds: 24},
		},
		Cuts: []domain.VideoHistoryCut{
			{CutIndex: 1, StartSec: 0, CharacterID: "zundamon"},
			{CutIndex: 2, StartSec: 8, CharacterID: "zundamon"},
			{CutIndex: 3, StartSec: 16, CharacterID: "zundamon"},
		},
	}
}

// TestRegenerateSectionKeyframesFormRendersSectionCuts verifies the section form renders only the
// selected section's cuts and posts back to that section's submit route.
func TestRegenerateSectionKeyframesFormRendersSectionCuts(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{detail: sectionRegenHistory()}

	req := newRegenerateSectionRequest(http.MethodGet, "/web/history/job-1/sections/0/regenerate", "job-1", "0", nil)
	rec := httptest.NewRecorder()

	h.RegenerateSectionKeyframesForm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("RegenerateSectionKeyframesForm status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	// モード切り替えはカット単位のフォームと共通の外部スクリプトが受け持ちます。
	for _, want := range []string{"Verse", "/web/history/job-1/sections/0/regenerate-keyframes", "Cut 1", "Cut 2",
		`src="/static/js/regenerate_mode.js"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("form body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Cut 3") {
		t.Fatalf("form body should not include the other section's cut: %s", body)
	}
}

// TestRegenerateSectionKeyframesFormRejectsUnknownSection verifies an out-of-range section index
// is a 404 rather than rendering an empty form.
func TestRegenerateSectionKeyframesFormRejectsUnknownSection(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{detail: sectionRegenHistory()}

	req := newRegenerateSectionRequest(http.MethodGet, "/web/history/job-1/sections/9/regenerate", "job-1", "9", nil)
	rec := httptest.NewRecorder()

	h.RegenerateSectionKeyframesForm(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// TestPostRegenerateSectionKeyframesEnqueuesSectionTask verifies the submit handler queues a
// section-scoped regeneration task carrying the original job's recipe URI and aspect ratio.
func TestPostRegenerateSectionKeyframesEnqueuesSectionTask(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	history := sectionRegenHistory()
	history.AspectRatio = "9:16"
	h.HistoryRepository = fakeHistoryRepository{detail: history}
	queue := &recordingQueue{}
	h.Queue = queue

	body := strings.NewReader("overwrite=on&edit_prompt=" + url.QueryEscape("もっと明るく"))
	req := newRegenerateSectionRequest(http.MethodPost, "/web/history/job-1/sections/1/regenerate-keyframes", "job-1", "1", body)
	rec := httptest.NewRecorder()

	h.PostRegenerateSectionKeyframes(rec, req)

	if queue.task == nil {
		t.Fatalf("no task enqueued; status=%d body=%s", rec.Code, rec.Body.String())
	}
	if queue.task.Command != domain.CommandRegenerateSectionKeyframes {
		t.Errorf("Command = %q, want %q", queue.task.Command, domain.CommandRegenerateSectionKeyframes)
	}
	if queue.task.SectionIndex == nil || *queue.task.SectionIndex != 1 {
		t.Errorf("SectionIndex = %v, want 1", queue.task.SectionIndex)
	}
	if queue.task.RecipeURL != history.StorageURI {
		t.Errorf("RecipeURL = %q, want %q", queue.task.RecipeURL, history.StorageURI)
	}
	if queue.task.VeoAspectRatio != "9:16" {
		t.Errorf("VeoAspectRatio = %q, want %q (inherited from history)", queue.task.VeoAspectRatio, "9:16")
	}
	if !queue.task.OverwriteKeyframe {
		t.Error("OverwriteKeyframe = false, want true")
	}
	if queue.task.EditPrompt != "もっと明るく" {
		t.Errorf("EditPrompt = %q, want %q", queue.task.EditPrompt, "もっと明るく")
	}
	if err := queue.task.Validate(); err != nil {
		t.Errorf("enqueued task failed validation: %v", err)
	}
}

// newRegenerateRequest builds a request carrying jobID/cutIndex chi route params.
func newRegenerateRequest(method, target, jobID, cutIndex string, body *strings.Reader) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, target, body)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", jobID)
	routeContext.URLParams.Add("cutIndex", cutIndex)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
}

// TestRegenerateCutKeyframeFormRendersCutData verifies the standalone regenerate-cut form
// pre-fills the cut's current prompt and shows which character a seed override would apply to.
func TestRegenerateCutKeyframeFormRendersCutData(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: "job-1"},
			Cuts: []domain.VideoHistoryCut{
				{CutIndex: 1, VisualAnchor: "original anchor", CharacterID: "zundamon"},
			},
		},
	}

	req := newRegenerateRequest(http.MethodGet, "/web/history/job-1/cuts/1/regenerate", "job-1", "1", nil)
	rec := httptest.NewRecorder()

	h.RegenerateCutKeyframeForm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("RegenerateCutKeyframeForm status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"original anchor", "zundamon", `src="/static/js/regenerate_mode.js"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("RegenerateCutKeyframeForm body missing %q: %s", want, body)
		}
	}
}

// TestRegenerateCutKeyframeFormPrefillsCharacterSeed verifies the seed input defaults to the
// cut's character's configured seed, so the user sees the current value rather than a blank field.
func TestRegenerateCutKeyframeFormPrefillsCharacterSeed(t *testing.T) {
	seed := int64(20260707)
	h, err := NewHandlerWithOptions(assets.Templates, nil, ModelOptions{}, CharacterOptions{
		Characters: []CharacterOption{{ID: "zundamon", Name: "Zundamon", Seed: &seed}},
	})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: "job-1"},
			Cuts: []domain.VideoHistoryCut{
				{CutIndex: 1, VisualAnchor: "original anchor", CharacterID: "zundamon"},
			},
		},
	}

	req := newRegenerateRequest(http.MethodGet, "/web/history/job-1/cuts/1/regenerate", "job-1", "1", nil)
	rec := httptest.NewRecorder()

	h.RegenerateCutKeyframeForm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("RegenerateCutKeyframeForm status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `value="20260707"`) {
		t.Fatalf("RegenerateCutKeyframeForm body missing prefilled seed: %s", rec.Body.String())
	}
}

// TestPostRegenerateCutKeyframeSkipsSeedOverrideWhenUnchanged verifies submitting the
// prefilled (unchanged) seed does not trigger a SeedOverride, avoiding an unnecessary
// per-task workflow rebuild when the user didn't actually change anything.
func TestPostRegenerateCutKeyframeSkipsSeedOverrideWhenUnchanged(t *testing.T) {
	seed := int64(20260707)
	queue := &recordingQueue{}
	h, err := NewHandlerWithOptions(assets.Templates, queue, ModelOptions{}, CharacterOptions{
		Characters: []CharacterOption{{ID: "zundamon", Name: "Zundamon", Seed: &seed}},
	})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: "job-1", StorageURI: "gs://bucket/jobs/job-1/video_music_meta.json"},
			Cuts: []domain.VideoHistoryCut{
				{CutIndex: 1, VisualAnchor: "original anchor", CharacterID: "zundamon"},
			},
		},
	}

	form := url.Values{"csrf_token": {"token"}, "seed": {"20260707"}}
	req := newRegenerateRequest(http.MethodPost, "/web/history/job-1/cuts/1/regenerate-keyframe", "job-1", "1", strings.NewReader(form.Encode()))
	req = req.WithContext(auth.WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostRegenerateCutKeyframe(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("PostRegenerateCutKeyframe status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if queue.task == nil {
		t.Fatal("queued task is nil")
	}
	if queue.task.SeedOverride != nil {
		t.Fatalf("queued SeedOverride = %v, want nil for unchanged seed", *queue.task.SeedOverride)
	}
}

// TestRegenerateCutKeyframeFormReturnsNotFoundForUnknownCut verifies a bad cut index 404s
// instead of rendering a blank form.
func TestRegenerateCutKeyframeFormReturnsNotFoundForUnknownCut(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: "job-1"},
			Cuts:         []domain.VideoHistoryCut{{CutIndex: 1}},
		},
	}

	req := newRegenerateRequest(http.MethodGet, "/web/history/job-1/cuts/9/regenerate", "job-1", "9", nil)
	rec := httptest.NewRecorder()

	h.RegenerateCutKeyframeForm(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("RegenerateCutKeyframeForm status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestPostRegenerateCutKeyframeAppliesOverridesAndOriginalJobID verifies the submit handler
// carries the prompt/seed overrides and the original job ID (needed so notifications link back
// to the job that actually receives the regenerated keyframe, not the synthetic regen job ID).
func TestPostRegenerateCutKeyframeAppliesOverridesAndOriginalJobID(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandler(assets.Templates, queue)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: "job-1", StorageURI: "gs://bucket/jobs/job-1/video_music_meta.json"},
			Cuts: []domain.VideoHistoryCut{
				{CutIndex: 1, VisualAnchor: "original anchor", CharacterID: "zundamon"},
			},
		},
	}

	form := url.Values{
		"csrf_token":    {"token"},
		"visual_anchor": {"new anchor text"},
		"seed":          {"12345"},
		"overwrite":     {"on"},
	}
	req := newRegenerateRequest(http.MethodPost, "/web/history/job-1/cuts/1/regenerate-keyframe", "job-1", "1", strings.NewReader(form.Encode()))
	req = req.WithContext(auth.WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostRegenerateCutKeyframe(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("PostRegenerateCutKeyframe status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if queue.task == nil {
		t.Fatal("queued task is nil")
	}
	if queue.task.OriginalJobID != "job-1" {
		t.Fatalf("queued OriginalJobID = %q, want job-1", queue.task.OriginalJobID)
	}
	if queue.task.VisualAnchorOverride != "new anchor text" {
		t.Fatalf("queued VisualAnchorOverride = %q, want %q", queue.task.VisualAnchorOverride, "new anchor text")
	}
	if queue.task.SeedOverride == nil || *queue.task.SeedOverride != 12345 {
		t.Fatalf("queued SeedOverride = %v, want 12345", queue.task.SeedOverride)
	}
	if queue.task.SeedOverrideCharacterID != "zundamon" {
		t.Fatalf("queued SeedOverrideCharacterID = %q, want zundamon", queue.task.SeedOverrideCharacterID)
	}
}

// TestPostRegenerateCutKeyframeRejectsSeedWithoutCharacter verifies a seed override is rejected
// rather than silently ignored when the cut has no character to apply it to.
func TestPostRegenerateCutKeyframeRejectsSeedWithoutCharacter(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandler(assets.Templates, queue)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: "job-1", StorageURI: "gs://bucket/jobs/job-1/video_music_meta.json"},
			Cuts:         []domain.VideoHistoryCut{{CutIndex: 1}},
		},
	}

	form := url.Values{"csrf_token": {"token"}, "seed": {"1"}}
	req := newRegenerateRequest(http.MethodPost, "/web/history/job-1/cuts/1/regenerate-keyframe", "job-1", "1", strings.NewReader(form.Encode()))
	req = req.WithContext(auth.WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostRegenerateCutKeyframe(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PostRegenerateCutKeyframe status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if queue.task != nil {
		t.Fatal("expected no task to be queued")
	}
}
