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
	"github.com/shouni/ap-mv/internal/ports"
)

type recordingQueue struct {
	task *domain.Task
}

// Enqueue adds a task to the queue.
func (q *recordingQueue) Enqueue(_ context.Context, task *domain.Task) error {
	q.task = task
	return nil
}

type fakeHistoryRepository struct {
	detail domain.VideoHistoryDetail
}

func (r fakeHistoryRepository) ListHistoryPage(context.Context, int, int) (domain.VideoHistoryPage, error) {
	return domain.VideoHistoryPage{}, nil
}

func (r fakeHistoryRepository) GetHistory(context.Context, string) (domain.VideoHistoryDetail, error) {
	return r.detail, nil
}

func (r fakeHistoryRepository) DeleteHistory(context.Context, string) error {
	return nil
}

func (r fakeHistoryRepository) DownloadKeyframes(context.Context, string, ports.KeyframeSink) error {
	return nil
}

func (r fakeHistoryRepository) KeyframeZipSignedURL(context.Context, string) (string, error) {
	return "", nil
}

// TestPostVideoRecipeCreateQueuesVideoRecipeCreate verifies that submissions queue video recipe creation.
func TestPostVideoRecipeCreateQueuesVideoRecipeCreate(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandlerWithOptions(assets.Templates, queue, ModelOptions{
		GeminiModels:       []string{"gemini-default", "gemini-alt"},
		ImageModels:        []string{"image-default", "image-alt"},
		DefaultGeminiModel: "gemini-default",
		DefaultImageModel:  "image-default",
	}, CharacterOptions{
		Characters: []CharacterOption{
			{ID: "zundamon", Name: "Zundamon"},
			{ID: "tsumugi", Name: "Tsumugi", IsDefault: true},
		},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	form := url.Values{
		"csrf_token":       {"token"},
		"music_recipe_url": {"gs://bucket/music_recipe.json"},
		"text_model":       {"gemini-alt"},
		"image_model":      {"image-alt"},
		"character_id":     {"zundamon"},
		"audio_url":        {"gs://bucket/music.mp3"},
	}
	req := httptest.NewRequest(http.MethodPost, "/web/video-recipe-create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostVideoRecipeCreate(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("PostVideoRecipeCreate status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Queued") {
		t.Fatalf("PostVideoRecipeCreate body missing queued page: %s", rec.Body.String())
	}
	if queue.task == nil {
		t.Fatal("queued task is nil")
	}
	if queue.task.Command != domain.CommandVideoRecipeCreate {
		t.Fatalf("queued command = %q, want %q", queue.task.Command, domain.CommandVideoRecipeCreate)
	}
	if queue.task.TextModel != "gemini-alt" {
		t.Fatalf("queued text model = %q, want gemini-alt", queue.task.TextModel)
	}
	if queue.task.ImageModel != "image-alt" {
		t.Fatalf("queued image model = %q, want image-alt", queue.task.ImageModel)
	}
	if queue.task.CharacterID != "zundamon" {
		t.Fatalf("queued character ID = %q, want zundamon", queue.task.CharacterID)
	}
	if queue.task.SourceURL != "gs://bucket/music_recipe.json" {
		t.Fatalf("queued source URL = %q, want music recipe URL", queue.task.SourceURL)
	}
	if queue.task.VisualMode != "default" {
		t.Fatalf("queued visual mode = %q, want default", queue.task.VisualMode)
	}
	if queue.task.AudioURL != "" {
		t.Fatalf("queued audio URL = %q, want empty for video recipe create", queue.task.AudioURL)
	}
}

// TestPostVideoRecipeCreateQueuesVisualMode verifies that visual mode submissions are preserved.
func TestPostVideoRecipeCreateQueuesVisualMode(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandlerWithOptions(assets.Templates, queue, ModelOptions{}, CharacterOptions{}, VisualModeOptions{
		Modes: []VisualModeOption{
			{ID: "default", Name: "Default", IsDefault: true},
			{ID: "sparkle_rock", Name: "Sparkle Rock"},
		},
		DefaultModeID: "default",
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	form := url.Values{
		"csrf_token":       {"token"},
		"music_recipe_url": {"gs://bucket/music_recipe.json"},
		"visual_mode":      {"sparkle_rock"},
	}
	req := httptest.NewRequest(http.MethodPost, "/web/video-recipe-create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostVideoRecipeCreate(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("PostVideoRecipeCreate status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if queue.task == nil {
		t.Fatal("queued task is nil")
	}
	if queue.task.VisualMode != "sparkle_rock" {
		t.Fatalf("queued visual mode = %q, want sparkle_rock", queue.task.VisualMode)
	}
}

// TestPostVideoRecipeCreateReturnsJSONWhenRequested verifies API clients can still request JSON.
func TestPostVideoRecipeCreateReturnsJSONWhenRequested(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandler(assets.Templates, queue)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	form := url.Values{
		"csrf_token":       {"token"},
		"music_recipe_url": {"gs://bucket/music_recipe.json"},
	}
	req := httptest.NewRequest(http.MethodPost, "/web/video-recipe-create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostVideoRecipeCreate(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("PostVideoRecipeCreate status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"queued"`) {
		t.Fatalf("PostVideoRecipeCreate JSON body = %s", rec.Body.String())
	}
}

// TestPostVideoRecipeCreateDefaultsToVideoRecipeCreate verifies that submissions use the video recipe creation command.
func TestPostVideoRecipeCreateDefaultsToVideoRecipeCreate(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandler(assets.Templates, queue)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	form := url.Values{
		"csrf_token": {"token"},
		"text":       {"source text"},
	}
	req := httptest.NewRequest(http.MethodPost, "/web/video-recipe-create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostVideoRecipeCreate(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("PostVideoRecipeCreate status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if queue.task == nil {
		t.Fatal("queued task is nil")
	}
	if queue.task.Command != domain.CommandVideoRecipeCreate {
		t.Fatalf("queued command = %q, want %q", queue.task.Command, domain.CommandVideoRecipeCreate)
	}
}

// TestPostRecipeAcceptsKeyframeVideoRecipeJSON verifies that MV generation accepts VideoRecipe JSON.
func TestPostRecipeAcceptsKeyframeVideoRecipeJSON(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandler(assets.Templates, queue, ModelOptions{
		GeminiModels:       []string{"gemini-a", "gemini-b"},
		ImageModels:        []string{"image-a", "image-b"},
		DefaultGeminiModel: "gemini-a",
		DefaultImageModel:  "image-a",
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	form := url.Values{
		"csrf_token":  {"token"},
		"text_model":  {"gemini-b"},
		"image_model": {"image-b"},
		"recipe_json": {`{
			"title": "mv",
			"cuts": [
				{"cut_index": 1, "duration_sec": 8, "visual_anchor": "blue stage", "keyframe_reference": "gs://bucket/keyframe.png"}
			]
		}`},
	}
	req := httptest.NewRequest(http.MethodPost, "/web/mv-from-keyframe-video-recipe", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostRecipe(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("PostRecipe status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if queue.task == nil {
		t.Fatal("queued task is nil")
	}
	if queue.task.Command != domain.CommandMVFromKeyframeVideoRecipe {
		t.Fatalf("queued command = %q, want %q", queue.task.Command, domain.CommandMVFromKeyframeVideoRecipe)
	}
	if queue.task.VideoRecipe == nil || len(queue.task.VideoRecipe.Cuts) != 1 {
		t.Fatalf("queued video recipe = %#v", queue.task.VideoRecipe)
	}
	if queue.task.TextModel != "gemini-b" {
		t.Fatalf("queued text model = %q, want %q", queue.task.TextModel, "gemini-b")
	}
	if queue.task.ImageModel != "image-b" {
		t.Fatalf("queued image model = %q, want %q", queue.task.ImageModel, "image-b")
	}
}

// TestHistoryDetailRendersKeyframeImage verifies history detail shows cut keyframes.
func TestHistoryDetailRendersKeyframeImage(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{
				JobID:    "video-recipe-20260618-081931-abc",
				Title:    "軌跡のアーキテクト",
				CutCount: 1,
			},
			Cuts: []domain.VideoHistoryCut{
				{
					CutIndex:          1,
					VisualAnchor:      "blue stage",
					KeyframeReference: "gs://bucket/keyframe.png",
					KeyframeURL:       "https://signed.example/keyframe.png",
					Status:            "pending",
				},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history/video-recipe-20260618-081931-abc", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "video-recipe-20260618-081931-abc")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"軌跡のアーキテクト",
		`src="https://signed.example/keyframe.png"`,
		"blue stage",
		"Cut 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("HistoryDetail body missing %q: %s", want, body)
		}
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
	for _, want := range []string{"original anchor", "zundamon"} {
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
	req = req.WithContext(WithCSRFToken(req.Context(), "token"))
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
	req = req.WithContext(WithCSRFToken(req.Context(), "token"))
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
	req = req.WithContext(WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostRegenerateCutKeyframe(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PostRegenerateCutKeyframe status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if queue.task != nil {
		t.Fatal("expected no task to be queued")
	}
}

// TestVideoRecipeCreateFormRendersModelSelects verifies that the video recipe form renders model selects.
func TestVideoRecipeCreateFormRendersModelSelects(t *testing.T) {
	h, err := NewHandlerWithOptions(assets.Templates, nil, ModelOptions{
		GeminiModels:       []string{"gemini-default", "gemini-alt"},
		ImageModels:        []string{"image-default", "image-alt"},
		DefaultGeminiModel: "gemini-default",
		DefaultImageModel:  "image-default",
	}, CharacterOptions{
		Characters: []CharacterOption{
			{ID: "zundamon", Name: "Zundamon"},
			{ID: "tsumugi", Name: "Tsumugi", IsDefault: true},
		},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/web/video-recipe-create", nil)
	req = req.WithContext(WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.VideoRecipeCreateForm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("VideoRecipeCreateForm status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`name="music_recipe_url"`,
		`name="text_model"`,
		`value="gemini-default" selected`,
		`value="gemini-alt"`,
		`name="image_model"`,
		`value="image-default" selected`,
		`value="image-alt"`,
		`name="character_id"`,
		`value="zundamon"`,
		`value="tsumugi" selected`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("VideoRecipeCreateForm body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `name="audio_url"`) {
		t.Fatalf("VideoRecipeCreateForm should not render audio_url input: %s", body)
	}
}
