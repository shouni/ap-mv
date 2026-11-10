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

// mediaHandler is a handler returning a job with one full set of media.
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

// Media entrances 302 to GCS signed URLs. It is the path that keeps signatures out of the page.
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
			// Unless the 302's cache is shorter than the signature's expiry, entries pointing at expired URLs remain.
			if got := rec.Header().Get("Cache-Control"); got != redirectCacheControl {
				t.Errorf("Cache-Control = %q, want %q", got, redirectCacheControl)
			}
		})
	}
}

// A cut not in the record is 404. It is the boundary that keeps "any valid job ID can sign any
// target" from happening.
func TestCutMediaRejectsUnknownCut(t *testing.T) {
	h := mediaHandler(t)

	rec := httptest.NewRecorder()
	h.CutVideo(rec, mediaRequest("/jobs/job-1/cuts/99/video",
		map[string]string{"jobID": "job-1", "cutIndex": "99"}))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// JSON callers (the MCP gateway) keep receiving signed URLs. They use the URLs themselves without
// following redirects, so emptying them here would leave the artifacts unreachable.
func TestHistoryDetailJSONKeepsSignedURLs(t *testing.T) {
	h := mediaHandler(t)

	req := mediaRequest("/jobs/job-1", map[string]string{"jobID": "job-1"})
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	h.Job(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// The status and detail are one document, with the detail nested under detail (jobDocument).
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
		t.Fatalf("cannot read the JSON: %v", err)
	}
	if doc.State != "succeeded" || doc.Detail == nil {
		t.Fatalf("the status and detail are not one document: %s", rec.Body.String())
	}
	payload := *doc.Detail

	if payload.SignedURL == "" || payload.FinalVideoSignedURL == "" {
		t.Errorf("no signed URL in the JSON: %s", rec.Body.String())
	}
	if len(payload.Cuts) != 1 || payload.Cuts[0].KeyframeURL == "" || payload.Cuts[0].VideoSignedURL == "" {
		t.Errorf("no signed URL for the cut: %s", rec.Body.String())
	}
	// Page paths are not put in JSON (json:"-" on the domain side).
	if strings.Contains(rec.Body.String(), "/jobs/job-1/cuts/") {
		t.Errorf("page paths are mixed into the JSON: %s", rec.Body.String())
	}
}
