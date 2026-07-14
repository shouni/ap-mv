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
	if strings.Contains(body, "完成動画") {
		t.Fatalf("HistoryDetail body unexpectedly rendered 完成動画 block without FinalVideoSignedURL: %s", body)
	}
}

// TestHistoryDetailAppliesAspectRatioClass verifies that a 9:16 job's cut thumbnails and the
// completed-video player get the vertical aspect-ratio treatment (CSS class / inline
// aspect-ratio), instead of always assuming the default 16:9 box — a 9:16 keyframe rendered
// into a forced 16:9 box gets cropped by object-fit: cover.
func TestHistoryDetailAppliesAspectRatioClass(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{
				JobID:               "job-1",
				Title:               "Test Short",
				AspectRatio:         "9:16",
				FinalVideoURL:       "gs://bucket/jobs/job-1/videos/final.mp4",
				FinalVideoSignedURL: "https://signed.example/final.mp4",
			},
			Cuts: []domain.VideoHistoryCut{
				{CutIndex: 1, KeyframeURL: "https://signed.example/cut1.png"},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history/job-1", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="card-img-top history-keyframe history-keyframe--9x16"`,
		"aspect-ratio: 9 / 16",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("HistoryDetail body missing %q for a 9:16 job: %s", want, body)
		}
	}
	if strings.Contains(body, "aspect-ratio: 16 / 9") {
		t.Fatalf("HistoryDetail body unexpectedly used the default 16:9 aspect ratio for a 9:16 job: %s", body)
	}
}

// TestHistoryDetailRendersFinalVideo verifies the history detail page shows the chain-finalize
// result (final_video_url) as a prominent player, distinct from the per-cut cards.
func TestHistoryDetailRendersFinalVideo(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{
				JobID:               "job-1",
				Title:               "Test MV",
				FinalVideoURL:       "gs://bucket/jobs/job-1/videos/final.mp4",
				FinalVideoSignedURL: "https://signed.example/final.mp4",
			},
			Cuts: []domain.VideoHistoryCut{
				{CutIndex: 1, VideoSignedURL: "https://signed.example/cut1.mp4"},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history/job-1", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"完成動画",
		`src="https://signed.example/final.mp4"`,
		"Copy MV Job ID",
		// JobID is rendered inside onclick="copyToClipboard('...')", a JS string-literal
		// context, so html/template would escape "/" as "\/" to defend against "</script>"-style
		// breakout sequences; job-1 has no such characters so it renders unescaped here.
		`copyToClipboard('job-1', this)`,
		"カット別詳細",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("HistoryDetail body missing %q: %s", want, body)
		}
	}
}
