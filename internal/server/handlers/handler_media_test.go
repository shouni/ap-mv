package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/domain"
)

// mediaHandler は、メディア 1 式を持つジョブを返すハンドラーです。
func mediaHandler(t *testing.T) *Handler {
	t.Helper()

	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{
				JobID:         "job-1",
				Title:         "Test MV",
				Generated:     true,
				StorageURI:    "gs://bucket/jobs/job-1/video_music_meta.json",
				FinalVideoURL: "gs://bucket/jobs/job-1/videos/final.mp4",
			},
			Cuts: []domain.VideoHistoryCut{
				{
					CutIndex:          1,
					KeyframeReference: "gs://bucket/jobs/job-1/images/cut1.png",
					VideoURL:          "gs://bucket/jobs/job-1/videos/cut1.mp4",
				},
			},
		},
	}
	return h
}

func mediaRequest(target string, params map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	routeContext := chi.NewRouteContext()
	for key, value := range params {
		routeContext.URLParams.Add(key, value)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
}

// メディアの入口は 302 で GCS の署名付き URL へ送ること。画面に署名を埋めないための経路です。
func TestMediaHandlersRedirectToSignedURL(t *testing.T) {
	h := mediaHandler(t)

	tests := []struct {
		name    string
		serve   func(http.ResponseWriter, *http.Request)
		target  string
		params  map[string]string
		wantSrc string
	}{
		{"metadata", h.JobMetadata, "/jobs/job-1/metadata",
			map[string]string{"jobID": "job-1"}, "video_music_meta.json"},
		{"final video", h.JobVideo, "/jobs/job-1/video",
			map[string]string{"jobID": "job-1"}, "videos/final.mp4"},
		{"cut video", h.CutVideo, "/jobs/job-1/cuts/1/video",
			map[string]string{"jobID": "job-1", "cutIndex": "1"}, "videos/cut1.mp4"},
		{"cut keyframe", h.CutKeyframe, "/jobs/job-1/cuts/1/keyframe",
			map[string]string{"jobID": "job-1", "cutIndex": "1"}, "images/cut1.png"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.serve(rec, mediaRequest(tt.target, tt.params))

			if rec.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302; body=%s", rec.Code, rec.Body.String())
			}
			location := rec.Header().Get("Location")
			if !strings.Contains(location, tt.wantSrc) {
				t.Errorf("Location = %q, want it to point at %q", location, tt.wantSrc)
			}
			// 302 のキャッシュは署名の期限より短くしないと、期限切れの URL を指すものが残ります。
			if got := rec.Header().Get("Cache-Control"); got != redirectCacheControl {
				t.Errorf("Cache-Control = %q, want %q", got, redirectCacheControl)
			}
		})
	}
}

// 記録に無いカットは 404 にすること。ジョブ ID さえ有効なら任意の対象を署名できる、
// という形にしないための境界です。
func TestCutMediaRejectsUnknownCut(t *testing.T) {
	h := mediaHandler(t)

	rec := httptest.NewRecorder()
	h.CutVideo(rec, mediaRequest("/jobs/job-1/cuts/99/video",
		map[string]string{"jobID": "job-1", "cutIndex": "99"}))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// JSON の呼び出し元（ap-mcp）は署名付き URL を受け取り続けること。
// リダイレクトを辿らずに URL 自体を使うため、ここを空にすると成果物へ到達できなくなります。
func TestHistoryDetailJSONKeepsSignedURLs(t *testing.T) {
	h := mediaHandler(t)

	req := mediaRequest("/jobs/job-1", map[string]string{"jobID": "job-1"})
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	h.Job(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// 状態と詳細は 1 つの文書で、詳細は detail の下に入れ子です（jobDocument）。
	var doc struct {
		State  string `json:"state"`
		Detail *struct {
			SignedURL           string `json:"signed_url"`
			FinalVideoSignedURL string `json:"final_video_signed_url"`
			Cuts                []struct {
				KeyframeURL    string `json:"keyframe_url"`
				VideoSignedURL string `json:"video_signed_url"`
			} `json:"cuts"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("JSON を読めない: %v", err)
	}
	if doc.State != "succeeded" || doc.Detail == nil {
		t.Fatalf("状態と詳細が 1 つの文書になっていません: %s", rec.Body.String())
	}
	payload := *doc.Detail

	if payload.SignedURL == "" || payload.FinalVideoSignedURL == "" {
		t.Errorf("JSON に署名付き URL がありません: %s", rec.Body.String())
	}
	if len(payload.Cuts) != 1 || payload.Cuts[0].KeyframeURL == "" || payload.Cuts[0].VideoSignedURL == "" {
		t.Errorf("カットの署名付き URL がありません: %s", rec.Body.String())
	}
	// 画面用のパスは JSON に出しません（domain 側で json:"-"）。
	if strings.Contains(rec.Body.String(), "/jobs/job-1/cuts/") {
		t.Errorf("画面用のパスが JSON に混ざっています: %s", rec.Body.String())
	}
}
