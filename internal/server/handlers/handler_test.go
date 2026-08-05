package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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

func (r fakeHistoryRepository) ListHistoryPage(context.Context, int, int) (domain.VideoHistoryPage, error) {
	return r.page, nil
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

func (r fakeHistoryRepository) InvalidateJob(string) {}

// TestLatestVideoForHomePrefersFinalVideoSignedURL verifies that when a job's chain-finalize
// result (FinalVideoSignedURL) is available, it is used instead of scanning cuts backward —
// scanning the last cut alone would show only the last chain's fragment for jobs with more than
// one continuation chain (see chain_finalize.go).
func TestLatestVideoForHomePrefersFinalVideoSignedURL(t *testing.T) {
	h, err := NewHandlerWithOptions(assets.Templates, &recordingQueue{}, ModelOptions{}, CharacterOptions{})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{
				JobID:               "job-1",
				Title:               "Test MV",
				Generated:           true,
				FinalVideoSignedURL: "https://signed.example/final.mp4",
			},
			Cuts: []domain.VideoHistoryCut{
				{CutIndex: 1, KeyframeURL: "https://signed.example/cut1.png", VideoSignedURL: "https://signed.example/cut1.mp4"},
				{CutIndex: 2, VideoSignedURL: "https://signed.example/cut2.mp4"},
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	got := h.latestVideoForHome(req, []domain.VideoHistory{{JobID: "job-1", Generated: true}})

	require.NotNil(t, got, "latestVideoForHome() = nil, want a result")
	if got.VideoURL != "https://signed.example/final.mp4" {
		t.Errorf("VideoURL = %q, want FinalVideoSignedURL", got.VideoURL)
	}
	if got.PosterURL != "https://signed.example/cut1.png" {
		t.Errorf("PosterURL = %q, want first cut's keyframe", got.PosterURL)
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
				{CutIndex: 1, VideoSignedURL: "https://signed.example/cut1.mp4"},
				{CutIndex: 2, KeyframeURL: "https://signed.example/cut2.png", VideoSignedURL: "https://signed.example/cut2.mp4"},
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	got := h.latestVideoForHome(req, []domain.VideoHistory{{JobID: "job-1", Generated: true}})

	require.NotNil(t, got, "latestVideoForHome() = nil, want a result")
	if got.VideoURL != "https://signed.example/cut2.mp4" {
		t.Errorf("VideoURL = %q, want last cut's VideoSignedURL", got.VideoURL)
	}
	if got.PosterURL != "https://signed.example/cut2.png" {
		t.Errorf("PosterURL = %q, want last cut's keyframe", got.PosterURL)
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
