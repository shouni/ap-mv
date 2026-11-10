package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"

	"github.com/shouni/go-serve-kit/respond"
)

// GenerateSectionVideo enqueues a task that generates only the given section's cuts in the order
// "keyframes → video" and **writes the result back to the original job**.
//
// It differs from the history detail's "video generation (target=section)" in where it writes.
// That one makes a standalone short fitted into 60 seconds as a new job; this one builds one MV up
// a section at a time, filling in the same job's recipe bit by bit. From a script-only job you can
// check the visuals on section 1 and move on if they look right.
//
// It does not join (create final_video_url). Re-joining every time a section is rendered would make
// the finished piece and work in progress indistinguishable under the same URL. Once every section
// is in place, Finalize puts them into one.
func (h *Handler) GenerateSectionVideo(w http.ResponseWriter, r *http.Request) {
	jobID, sectionIndex, err := parseJobIDAndSectionIndex(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, ok := h.loadHistoryForMutation(w, r, jobID)
	if !ok {
		return
	}
	group, ok := findHistorySectionGroup(history, sectionIndex)
	if !ok {
		respond.Error(w, r, http.StatusNotFound, "section not found")
		return
	}
	// Check before enqueueing. Without rejecting here, it would only show up as a job that failed having generated nothing.
	if len(group.Cuts) == 0 {
		respond.Error(w, r, http.StatusBadRequest, "section has no cuts to generate")
		return
	}
	// The task's own JobID is newly minted so Cloud Tasks task names never collide. The artifacts'
	// destination is pointed back at the original job by OriginalJobOutputFilter, so minting here is
	// only for the queue's sake, and no new job directory appears on GCS.
	newJobID, ok := mintJobID(w, r, "section-video")
	if !ok {
		return
	}
	task := &domain.Task{
		JobID:        newJobID,
		Command:      domain.CommandSectionVideo,
		RecipeURL:    history.StorageURI,
		SectionIndex: &sectionIndex,
		VeoModel:     h.veoModelFromForm(r),
		// The aspect ratio always carries over the value decided once when the keyframes were made
		// (the same reason as RegenerateCutVideo).
		VeoAspectRatio: history.AspectRatio,
		OriginalJobID:  jobID,
		CreatedAt:      time.Now().UTC(),
	}
	h.enqueue(w, r, task)
}

// Finalize enqueues a task that joins the generated cuts' videos into one finished video.
// It generates nothing, so there is no extra charge; it finishes what section_video built up.
func (h *Handler) Finalize(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, ok := h.loadHistoryForMutation(w, r, jobID)
	if !ok {
		return
	}
	newJobID, ok := mintJobID(w, r, "finalize")
	if !ok {
		return
	}
	task := &domain.Task{
		JobID:          newJobID,
		Command:        domain.CommandFinalizeVideo,
		RecipeURL:      history.StorageURI,
		VeoAspectRatio: history.AspectRatio,
		OriginalJobID:  jobID,
		CreatedAt:      time.Now().UTC(),
	}
	h.enqueue(w, r, task)
}
