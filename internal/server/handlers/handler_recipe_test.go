package handlers

import (
	"github.com/shouni/gcp-kit/auth"

	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/domain"
)

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
	h.MusicBucket = "ap-music"

	form := url.Values{
		"csrf_token":   {"token"},
		"music_job_id": {"20260711132823-256e9128"},
		"text_model":   {"gemini-alt"},
		"image_model":  {"image-alt"},
		"character_id": {"zundamon"},
		"audio_url":    {"gs://bucket/music.mp3"},
	}
	req := httptest.NewRequest(http.MethodPost, "/web/video-recipe-create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostVideoRecipeCreate(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("PostVideoRecipeCreate status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Queued") {
		t.Fatalf("PostVideoRecipeCreate body missing queued page: %s", rec.Body.String())
	}
	// 投入後の画面はジョブ状態をポーリングして queued → running → succeeded/failed を
	// 表示する（サーバー側の /web/jobs/{jobID} は以前から存在し、UI 側が未接続だった）。
	// スクリプトは外部ファイルなので、読み込みとジョブ ID の受け渡しの両方を確認する。
	for _, want := range []string{`src="/static/js/job_status.js"`, `data-job-id="`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("queued page is missing %s: %s", want, rec.Body.String())
		}
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
	if queue.task.SourceURL != "gs://ap-music/music/20260711132823-256e9128/recipe.json" {
		t.Fatalf("queued source URL = %q, want music recipe URL derived from music_job_id", queue.task.SourceURL)
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
	h.MusicBucket = "ap-music"

	form := url.Values{
		"csrf_token":   {"token"},
		"music_job_id": {"20260711132823-256e9128"},
		"visual_mode":  {"sparkle_rock"},
	}
	req := httptest.NewRequest(http.MethodPost, "/web/video-recipe-create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.WithCSRFToken(req.Context(), "token"))
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

// TestPostVideoRecipeCreateFallsBackToURLForM2M verifies the raw `url` form field (used by
// ap-mcp's compose_video tool, which also accepts plain text/image sources unrelated to a music
// job) still works when music_job_id is absent.
func TestPostVideoRecipeCreateFallsBackToURLForM2M(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandler(assets.Templates, queue)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	form := url.Values{
		"csrf_token": {"token"},
		"url":        {"gs://bucket/source.json"},
	}
	req := httptest.NewRequest(http.MethodPost, "/web/video-recipe-create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostVideoRecipeCreate(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("PostVideoRecipeCreate status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if queue.task == nil || queue.task.SourceURL != "gs://bucket/source.json" {
		t.Fatalf("queued source URL = %v, want gs://bucket/source.json", queue.task)
	}
}

// TestPostVideoRecipeCreateRejectsInvalidMusicJobID verifies a malformed music_job_id is
// rejected rather than silently building a broken GCS path.
func TestPostVideoRecipeCreateRejectsInvalidMusicJobID(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandler(assets.Templates, queue)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.MusicBucket = "ap-music"

	form := url.Values{
		"csrf_token":   {"token"},
		"music_job_id": {"not a valid id!"},
	}
	req := httptest.NewRequest(http.MethodPost, "/web/video-recipe-create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.PostVideoRecipeCreate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PostVideoRecipeCreate status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if queue.task != nil {
		t.Fatal("task should not have been queued for an invalid music_job_id")
	}
}

// TestPostVideoRecipeCreateReturnsJSONWhenRequested verifies API clients can still request JSON.
func TestPostVideoRecipeCreateReturnsJSONWhenRequested(t *testing.T) {
	queue := &recordingQueue{}
	h, err := NewHandler(assets.Templates, queue)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.MusicBucket = "ap-music"

	form := url.Values{
		"csrf_token":   {"token"},
		"music_job_id": {"20260711132823-256e9128"},
	}
	req := httptest.NewRequest(http.MethodPost, "/web/video-recipe-create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(auth.WithCSRFToken(req.Context(), "token"))
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
	req = req.WithContext(auth.WithCSRFToken(req.Context(), "token"))
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
	req = req.WithContext(auth.WithCSRFToken(req.Context(), "token"))
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

// TestComposeTemplateOffersAllAspectRatios は、compose.html の選択肢が
// domain.AllowedAspectRatios（唯一の定義）と同期していることを検証します。
// テンプレートは Go の定数を参照できないため、このテストが同期の担保です。
func TestComposeTemplateOffersAllAspectRatios(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	rec := httptest.NewRecorder()
	h.VideoRecipeCreateForm(rec, httptest.NewRequest(http.MethodGet, "/web/video-recipe-create", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("VideoRecipeCreateForm status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, ratio := range domain.AllowedAspectRatios {
		if !strings.Contains(body, `value="`+ratio+`"`) {
			t.Errorf("compose.html is missing aspect ratio option %q (sync it with domain.AllowedAspectRatios)", ratio)
		}
	}
}
