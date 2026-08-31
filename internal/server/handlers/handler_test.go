package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
	"github.com/shouni/gcp-kit/auth/session"
)

type recordingQueue struct {
	task *domain.Task
}

// Enqueue adds a task to the queue.
func (q *recordingQueue) Enqueue(_ context.Context, task *domain.Task) error {
	q.task = task
	return nil
}

// EnqueueWithName adds a task to the queue, ignoring taskID (tests don't need idempotency).
func (q *recordingQueue) EnqueueWithName(_ context.Context, _ string, task *domain.Task) error {
	q.task = task
	return nil
}

type fakeHistoryRepository struct {
	detail domain.VideoHistoryDetail
	page   domain.VideoHistoryPage
	// usage は nil のままなら「実績記録なし」を表します（実績記録の導入前に走ったジョブ）。
	usage    *domain.VeoUsage
	usageErr error
}

func (r fakeHistoryRepository) GetVeoUsage(context.Context, string) (*domain.VeoUsage, error) {
	return r.usage, r.usageErr
}

func (r fakeHistoryRepository) ListHistoryPage(context.Context, int, int, domain.JobStage) (domain.VideoHistoryPage, error) {
	return r.page, nil
}

func (r fakeHistoryRepository) GetHistory(context.Context, string) (domain.VideoHistoryDetail, error) {
	return r.detail, nil
}

func (r fakeHistoryRepository) GetRecipe(context.Context, string) (*domain.VideoRecipe, error) {
	return &domain.VideoRecipe{}, nil
}

func (r fakeHistoryRepository) SaveRecipe(context.Context, string, *domain.VideoRecipe) error {
	return nil
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

// SignHistoryURLs は JSON 応答のためだけの経路です。署名の中身はここでは問わず、
// 「画面は署名を要求しない」ことを確かめられれば足りるので、目印だけ入れます。
func (r fakeHistoryRepository) SignHistoryURLs(_ context.Context, detail *domain.VideoHistoryDetail) error {
	if detail == nil {
		return nil
	}
	detail.SignedURL = "https://signed.example/metadata.json"
	if detail.FinalVideoURL != "" {
		detail.FinalVideoSignedURL = "https://signed.example/final.mp4"
	}
	for i := range detail.Cuts {
		if detail.Cuts[i].VideoURL != "" {
			detail.Cuts[i].VideoSignedURL = "https://signed.example/cut.mp4"
		}
		if detail.Cuts[i].KeyframeReference != "" {
			detail.Cuts[i].KeyframeURL = "https://signed.example/cut.png"
		}
	}
	return nil
}

func (r fakeHistoryRepository) SignedObjectURL(_ context.Context, uri string) (string, error) {
	if uri == "" {
		return "", nil
	}
	return "https://signed.example/object?src=" + uri, nil
}

// TestLatestVideoForHomePrefersFinalVideo verifies that when a job's chain-finalize result
// (FinalVideoURL) is available, it is used instead of scanning cuts backward — scanning the
// last cut alone would show only the last chain's fragment for jobs with more than one
// continuation chain (see chain_finalize.go).
//
// 出るのは同一オリジンのパスです。署名付き URL は画面に埋めません。
func TestLatestVideoForHomePrefersFinalVideo(t *testing.T) {
	h, err := NewHandlerWithOptions(assets.Templates, &recordingQueue{}, ModelOptions{}, CharacterOptions{})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{
				JobID:         "job-1",
				Title:         "Test MV",
				Generated:     true,
				FinalVideoURL: "gs://bucket/jobs/job-1/videos/final.mp4",
			},
			Cuts: []domain.VideoHistoryCut{
				{CutIndex: 1, KeyframeReference: "gs://bucket/jobs/job-1/images/cut1.png", VideoURL: "gs://bucket/jobs/job-1/videos/cut1.mp4"},
				{CutIndex: 2, VideoURL: "gs://bucket/jobs/job-1/videos/cut2.mp4"},
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	got := h.latestVideoForHome(req, []domain.VideoHistory{{JobID: "job-1", Generated: true}})

	if got == nil {
		t.Fatal("latestVideoForHome() = nil, want a result")
	}
	if got.VideoURL != "/history/job-1/video" {
		t.Errorf("VideoURL = %q, want the final-video web path", got.VideoURL)
	}
	if got.PosterURL != "/history/job-1/cuts/1/keyframe" {
		t.Errorf("PosterURL = %q, want the first cut's keyframe web path", got.PosterURL)
	}
}

// TestLatestVideoForHomeFallsBackToLastCutWithoutFinalVideo verifies backward compatibility for
// jobs generated before final_video_url existed: scanning cuts from the end still works.
func TestLatestVideoForHomeFallsBackToLastCutWithoutFinalVideo(t *testing.T) {
	h, err := NewHandlerWithOptions(assets.Templates, &recordingQueue{}, ModelOptions{}, CharacterOptions{})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: "job-1", Title: "Test MV", Generated: true},
			Cuts: []domain.VideoHistoryCut{
				{CutIndex: 1, VideoURL: "gs://bucket/jobs/job-1/videos/cut1.mp4"},
				{CutIndex: 2, KeyframeReference: "gs://bucket/jobs/job-1/images/cut2.png", VideoURL: "gs://bucket/jobs/job-1/videos/cut2.mp4"},
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	got := h.latestVideoForHome(req, []domain.VideoHistory{{JobID: "job-1", Generated: true}})

	if got == nil {
		t.Fatal("latestVideoForHome() = nil, want a result")
	}
	if got.VideoURL != "/history/job-1/cuts/2/video" {
		t.Errorf("VideoURL = %q, want the last cut's video web path", got.VideoURL)
	}
	if got.PosterURL != "/history/job-1/cuts/2/keyframe" {
		t.Errorf("PosterURL = %q, want the last cut's keyframe web path", got.PosterURL)
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

	req := httptest.NewRequest(http.MethodGet, "/video-recipe-create", nil)
	req = req.WithContext(session.WithCSRFToken(req.Context(), "token"))
	rec := httptest.NewRecorder()

	h.VideoRecipeCreateForm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("VideoRecipeCreateForm status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, want := range []string{
		// CSRF トークンはテンプレートが出す側の責務。検証は gcp-kit の
		// セッションミドルウェアが持つので、ここは埋め込み漏れだけを見ます。
		`name="csrf_token" value="token"`,
		`name="music_job_id"`,
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
