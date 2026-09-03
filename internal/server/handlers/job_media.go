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

// 画面が指すのは、この 4 つの同一オリジンのパスです。GCS の署名付き URL は HTML に出さず、
// ここへ来たリクエストをハンドラーが 302 で送り出します。
//
// 署名を HTML に埋めていた頃は 2 つの問題がありました。ひとつは、その URL が期限内は
// アプリの認証の外側で使えること。もうひとつは期限切れで、詳細画面はカットを見比べて
// 作り直す判断をする画面なので滞在が長く、開いたまま期限を過ぎると再生ボタンが 403 に
// なって手動リロードするまで戻りませんでした。リダイレクトなら毎回その場で発行します。
//
// 副次的に、詳細 1 画面あたり数十回あった IAM SignBlob の前払いも無くなります
// （Cloud Run は秘密鍵を持たないため、署名はローカル計算ではなくネットワーク往復です）。
const (
	mediaPathMetadata = "metadata"
	mediaPathVideo    = "video"
	mediaPathKeyframe = "keyframe"
)

// redirectCacheControl は 302 自体の寿命です。署名付き URL の期限より短くして、
// キャッシュされたリダイレクトが期限切れの URL を指さないようにします。
const redirectCacheControl = "private, max-age=600"

// MetadataWebPath などは、画面が辿る同一オリジンのパスを組み立てます。
func MetadataWebPath(jobID string) string {
	return "/jobs/" + url.PathEscape(jobID) + "/" + mediaPathMetadata
}

// FinalVideoWebPath は結合済み完成動画のパスです。
func FinalVideoWebPath(jobID string) string {
	return "/jobs/" + url.PathEscape(jobID) + "/" + mediaPathVideo
}

// CutVideoWebPath はカット単体の動画のパスです。
func CutVideoWebPath(jobID string, cutIndex int) string {
	return cutAssetWebPath(jobID, cutIndex, mediaPathVideo)
}

// CutKeyframeWebPath はカットのキーフレーム画像のパスです。
func CutKeyframeWebPath(jobID string, cutIndex int) string {
	return cutAssetWebPath(jobID, cutIndex, mediaPathKeyframe)
}

func cutAssetWebPath(jobID string, cutIndex int, kind string) string {
	return fmt.Sprintf("/jobs/%s/cuts/%d/%s", url.PathEscape(jobID), cutIndex, kind)
}

// applyWebMediaURLs は、画面が辿るパスを履歴詳細へ埋めます。
// 署名付き URL とは別のフィールドで、JSON には出ません（domain 側で json:"-"）。
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

// redirectJobAsset は、ジョブの履歴から 1 つの gs:// URI を取り出して署名し、302 で送ります。
//
// 署名するのは履歴に記録されていた URI だけです。呼び出し元が渡した文字列をそのまま
// 署名すると、ジョブ ID さえ有効なら任意のオブジェクトの URL を発行できてしまいます。
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
		slog.ErrorContext(r.Context(), "failed to load history for asset redirect", "job_id", jobID, "error", err)
		respond.Error(w, r, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
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
		slog.ErrorContext(r.Context(), "failed to sign asset URL", "job_id", jobID, "uri", uri, "error", err)
		respond.Error(w, r, http.StatusInternalServerError, "failed to build the asset URL")
		return
	}

	w.Header().Set("Cache-Control", redirectCacheControl)
	http.Redirect(w, r, signedURL, http.StatusFound)
}

// JobKeyframes は、キーフレーム一式の zip を返します（GET /jobs/{jobID}/keyframes）。
// 事前に作られた zip の署名付き URL へ 302 し、
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
		slog.ErrorContext(r.Context(), "failed to get history for keyframe download", "job_id", jobID, "error", err)
		respond.Error(w, r, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
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
