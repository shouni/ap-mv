package handlers

import (
	"log/slog"
	"net/http"
	"slices"

	"github.com/shouni/ap-mv/internal/domain"
)

// homeRecentJobs はホームに表示する直近ジョブ件数です。
const homeRecentJobs = 10

// Home renders the home page with the most recent jobs and the latest generated video.
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	data := PageData{Title: "Home"}
	if h.HistoryRepository != nil {
		page, err := h.HistoryRepository.ListHistoryPage(r.Context(), 1, homeRecentJobs, "")
		if err != nil {
			slog.ErrorContext(r.Context(), "failed to load recent history for home", "error", err)
		} else {
			data.HistoryItems = page.Items
			data.LatestVideo = h.latestVideoForHome(r, page.Items)
		}
	}
	h.renderPage(w, r, data, "index.html")
}

// latestVideoForHome は直近ジョブのうち最初に動画を持つジョブから、ホーム掲載用の
// 再生情報を返します。見つからなければ nil を返します。
func (h *Handler) latestVideoForHome(r *http.Request, items []domain.VideoHistory) *HomeLatestVideo {
	for _, item := range items {
		if !item.Generated {
			continue
		}
		detail, err := h.HistoryRepository.GetHistory(r.Context(), item.JobID)
		if err != nil {
			slog.WarnContext(r.Context(), "failed to load latest video for home",
				"job_id", item.JobID,
				"error", err,
			)
			return nil
		}
		// FinalVideoURL（継続チェーンをハードカットで結合した完成動画、chain_finalize.go）が
		// あればそれを最優先で使う。無い場合（final_video_url追加より前に生成された旧ジョブ）
		// は、末尾カットから探して最初に見つかった完成版を使う旧来のフォールバックに戻る。
		// 注意: 旧フォールバックは「チェーンが1本だけ」という前提に基づいており、チェーンが
		// リセットされたジョブでは末尾チェーンの断片だけが表示されてしまう
		// （video_music_meta.jsonがfinal_video_urlを持つ新しいジョブでは発生しない）。
		// 指すのは同一オリジンのパスです（署名は 302 の時点で 1 本だけ）。
		applyWebMediaURLs(&detail)
		if detail.FinalVideoWebURL != "" {
			poster := ""
			if len(detail.Cuts) > 0 {
				poster = detail.Cuts[0].KeyframeWebURL
			}
			return &HomeLatestVideo{
				JobID:     detail.JobID,
				Title:     detail.Title,
				VideoURL:  detail.FinalVideoWebURL,
				PosterURL: poster,
			}
		}
		for _, cut := range slices.Backward(detail.Cuts) {

			if cut.VideoWebURL != "" {
				return &HomeLatestVideo{
					JobID:     detail.JobID,
					Title:     detail.Title,
					VideoURL:  cut.VideoWebURL,
					PosterURL: cut.KeyframeWebURL,
				}
			}
		}
		// 最新の生成済みジョブに再生可能な動画がなければ、それ以上は遡らない
		// （表示ごとに GetHistory を積み重ねない）。
		return nil
	}
	return nil
}

// ComposeForm は VideoRecipe 作成フォームです（GET /compose）。
// 送り先は POST /jobs で、押したボタンが command（本生成か台本のみか）を決めます。
func (h *Handler) ComposeForm(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, r, h.withModelOptions(PageData{
		Title:     "Video Recipe Create",
		CSRFToken: csrfTokenFromContext(r.Context()),
	}), "compose.html")
}
