package handlers

import (
	"archive/zip"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"

	"github.com/shouni/go-serve-kit/respond"
)

// The page points at these four same-origin paths. GCS signed URLs are not put in HTML; the
// handler 302s the requests that arrive here.
//
// Embedding signatures in HTML had two problems. One, the URL could be used outside the app's
// authentication until it expired. Two, expiry: the detail page is where cuts are compared to
// decide what to remake, so people stay on it a long time, and once it sat open past the expiry
// the play button gave 403 until a manual reload. A redirect issues one on the spot every time.
//
// As a side effect, the dozens of IAM SignBlob calls prepaid per detail page are gone too
// (Cloud Run has no private key, so signing is a network round trip, not a local computation).
const (
	mediaPathMetadata = "metadata"
	mediaPathVideo    = "video"
	mediaPathKeyframe = "keyframe"
)

// redirectCacheControl is the lifetime of the 302 itself. It is shorter than the signed URL's
// expiry so a cached redirect never points at an expired URL.
const redirectCacheControl = "private, max-age=600"

// MetadataWebPath and the others build the same-origin paths the page follows.
func MetadataWebPath(jobID string) string {
	return "/jobs/" + url.PathEscape(jobID) + "/" + mediaPathMetadata
}

// FinalVideoWebPath is the path of the joined finished video.
func FinalVideoWebPath(jobID string) string {
	return "/jobs/" + url.PathEscape(jobID) + "/" + mediaPathVideo
}

// CutVideoWebPath is the path of a single cut's video.
func CutVideoWebPath(jobID string, cutIndex int) string {
	return cutAssetWebPath(jobID, cutIndex, mediaPathVideo)
}

// CutKeyframeWebPath is the path of a cut's keyframe image.
func CutKeyframeWebPath(jobID string, cutIndex int) string {
	return cutAssetWebPath(jobID, cutIndex, mediaPathKeyframe)
}

func cutAssetWebPath(jobID string, cutIndex int, kind string) string {
	return fmt.Sprintf("/jobs/%s/cuts/%d/%s", url.PathEscape(jobID), cutIndex, kind)
}

// applyWebMediaURLs fills the paths the page follows into the history detail.
// They are fields separate from the signed URLs and are not put in JSON (json:"-" on the domain side).
func applyWebMediaURLs(detail *domain.VideoHistoryDetail) {
	if detail == nil {
		return
	}
	if strings.TrimSpace(detail.StorageURI) != "" {
		detail.MetadataWebURL = MetadataWebPath(detail.JobID)
	}
	if strings.TrimSpace(detail.FinalVideoURL) != "" {
		detail.FinalVideoWebURL = FinalVideoWebPath(detail.JobID)
	}
	for i := range detail.Cuts {
		cut := &detail.Cuts[i]
		if strings.TrimSpace(cut.VideoURL) != "" {
			cut.VideoWebURL = CutVideoWebPath(detail.JobID, cut.CutIndex)
		}
		if strings.TrimSpace(cut.KeyframeReference) != "" {
			cut.KeyframeWebURL = CutKeyframeWebPath(detail.JobID, cut.CutIndex)
		}
	}
}

// JobMetadata redirects to the signed URL of the job's metadata JSON.
func (h *Handler) JobMetadata(w http.ResponseWriter, r *http.Request) {
	h.redirectJobAsset(w, r, func(detail domain.VideoHistoryDetail) (string, error) {
		return detail.StorageURI, nil
	})
}

// JobVideo redirects to the signed URL of the job's finalized video.
func (h *Handler) JobVideo(w http.ResponseWriter, r *http.Request) {
	h.redirectJobAsset(w, r, func(detail domain.VideoHistoryDetail) (string, error) {
		return detail.FinalVideoURL, nil
	})
}

// CutVideo redirects to the signed URL of one cut's video.
func (h *Handler) CutVideo(w http.ResponseWriter, r *http.Request) {
	h.redirectCutAsset(w, r, func(cut domain.VideoHistoryCut) string { return cut.VideoURL })
}

// CutKeyframe redirects to the signed URL of one cut's keyframe image.
func (h *Handler) CutKeyframe(w http.ResponseWriter, r *http.Request) {
	h.redirectCutAsset(w, r, func(cut domain.VideoHistoryCut) string { return cut.KeyframeReference })
}

func (h *Handler) redirectCutAsset(w http.ResponseWriter, r *http.Request, pick func(domain.VideoHistoryCut) string) {
	h.redirectJobAsset(w, r, func(detail domain.VideoHistoryDetail) (string, error) {
		raw := strings.TrimSpace(chi.URLParam(r, "cutIndex"))
		cutIndex, err := strconv.Atoi(raw)
		if err != nil {
			return "", fmt.Errorf("cut index must be a number: %q", raw)
		}
		for _, cut := range detail.Cuts {
			if cut.CutIndex == cutIndex {
				return pick(cut), nil
			}
		}
		return "", fmt.Errorf("cut %d not found", cutIndex)
	})
}

// redirectJobAsset takes one gs:// URI out of the job's history, signs it and sends a 302.
//
// Only URIs recorded in history are signed. Signing the string the caller passed as-is would let
// anyone with a valid job ID issue URLs for arbitrary objects.
func (h *Handler) redirectJobAsset(w http.ResponseWriter, r *http.Request, pick func(domain.VideoHistoryDetail) (string, error)) {
	if h.HistoryRepository == nil {
		respond.Error(w, r, http.StatusServiceUnavailable, "history storage adapter is not configured")
		return
	}
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}

	detail, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		respond.ServerError(w, r, http.StatusInternalServerError, err, "failed to load history for asset redirect", "job_id", jobID)
		return
	}

	uri, err := pick(detail)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, err.Error())
		return
	}
	if strings.TrimSpace(uri) == "" {
		respond.Error(w, r, http.StatusNotFound, "asset is not available for this job")
		return
	}

	signedURL, err := h.HistoryRepository.SignedObjectURL(r.Context(), uri)
	if err != nil || strings.TrimSpace(signedURL) == "" {
		respond.ServerError(w, r, http.StatusInternalServerError, err, "failed to sign asset URL", "job_id", jobID, "uri", uri)
		return
	}

	w.Header().Set("Cache-Control", redirectCacheControl)
	http.Redirect(w, r, signedURL, http.StatusFound)
}

// JobKeyframes returns the zip of all keyframes (GET /jobs/{jobID}/keyframes).
// It 302s to the signed URL of the pre-built zip and
// Falls back to on-demand zip generation for jobs that predate the pipeline change.
func (h *Handler) JobKeyframes(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.HistoryRepository == nil {
		respond.Error(w, r, http.StatusServiceUnavailable, "history storage adapter is not configured")
		return
	}

	// Try to redirect to the pre-built zip uploaded by the pipeline.
	// Continue to on-demand fallback even on signing error — intentional fail-soft.
	signedURL, err := h.HistoryRepository.KeyframeZipSignedURL(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "keyframe zip signed URL failed, falling back to on-demand generation", "job_id", jobID, "error", err)
	}
	if signedURL != "" {
		http.Redirect(w, r, signedURL, http.StatusFound)
		return
	}

	// Fall back: build zip on-demand for jobs without a pre-built zip.
	history, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		respond.ServerError(w, r, http.StatusInternalServerError, err, "failed to get history for keyframe download", "job_id", jobID)
		return
	}
	hasKeyframes := false
	for _, cut := range history.Cuts {
		if strings.TrimSpace(cut.KeyframeReference) != "" {
			hasKeyframes = true
			break
		}
	}
	if !hasKeyframes {
		respond.Error(w, r, http.StatusNotFound, "no keyframes available")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="keyframes-%s.zip"`, jobID))
	zw := zip.NewWriter(w)
	if err := h.HistoryRepository.DownloadKeyframes(r.Context(), jobID, func(name string, reader io.Reader) error {
		fw, err := zw.Create(name)
		if err != nil {
			slog.ErrorContext(r.Context(), "failed to create zip entry", "job_id", jobID, "file", name, "error", err)
			return err
		}
		if _, err := io.Copy(fw, reader); err != nil {
			slog.ErrorContext(r.Context(), "failed to write zip entry", "job_id", jobID, "file", name, "error", err)
			return err
		}
		return nil
	}); err != nil {
		slog.ErrorContext(r.Context(), "failed to stream keyframe zip", "job_id", jobID, "error", err)
		return
	}
	if assContent := domain.GenerateASS(history.Cuts, domain.ASSColors{}, history.Tempo); assContent != "" {
		fw, err := zw.Create("lyrics.ass")
		if err != nil {
			slog.ErrorContext(r.Context(), "failed to create ass zip entry", "job_id", jobID, "error", err)
		} else if _, err := io.WriteString(fw, assContent); err != nil {
			slog.ErrorContext(r.Context(), "failed to write ass zip entry", "job_id", jobID, "error", err)
		}
	}
	if err := zw.Close(); err != nil {
		slog.ErrorContext(r.Context(), "failed to finalize zip", "job_id", jobID, "error", err)
	}
}
