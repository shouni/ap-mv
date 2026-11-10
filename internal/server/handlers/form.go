package handlers

import (
	"log/slog"
	"net/http"
	"slices"

	"github.com/shouni/ap-mv/internal/domain"
)

// homeRecentJobs is the number of recent jobs shown on the home page.
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

// latestVideoForHome returns the playback information for the home page from the first of the
// recent jobs that has a video. It returns nil when none is found.
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
		// If there is a FinalVideoURL (the finished video joining the continuation chain with hard cuts,
		// chain_finalize.go), use it first. Without one (old jobs generated before final_video_url was
		// added), fall back to the old approach of searching from the last cut and using the first finished
		// one found. Note: the old fallback assumes "only one chain", so for jobs whose chain was reset only
		// a fragment of the last chain is shown (this does not happen for newer jobs whose
		// video_music_meta.json has final_video_url).
		// It points at a same-origin path (exactly one URL is signed, at the 302).
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
		// If the latest generated job has no playable video, do not go further back
		// (do not stack up GetHistory calls per page view).
		return nil
	}
	return nil
}

// ComposeForm is the VideoRecipe creation form (GET /compose).
// It posts to POST /jobs, and the button pressed decides command (full generation or script only).
func (h *Handler) ComposeForm(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, r, h.withModelOptions(PageData{
		Title:     "Video Recipe Create",
		CSRFToken: csrfTokenFromContext(r.Context()),
	}), "compose.html")
}
