package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-utils/jobid"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"

	"github.com/shouni/go-serve-kit/respond"
)

// maxRecipeJSONSize caps the accepted recipe body. Mirrors the pipeline's read limit
// (filter.maxRecipeJSONSize): it is the same document read from the other direction.
const maxRecipeJSONSize = 5 * 1024 * 1024

// JobRecipe returns a job's stored VideoRecipe as {"job_id": ..., "recipe": {...}}.
//
// It is a separate path from the history detail formatted for display. What it returns can be
// edited and sent straight back to JobRecipeUpdate, with no display-only values such as signed URLs or estimated costs mixed in.
func (h *Handler) JobRecipe(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.HistoryRepository == nil {
		respond.Error(w, r, http.StatusInternalServerError, "history storage adapter is not configured")
		return
	}
	recipe, err := h.HistoryRepository.GetRecipe(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to get job recipe", "job_id", jobID, "error", err)
		respond.Error(w, r, http.StatusNotFound, "job not found")
		return
	}
	respond.JSON(w, r, http.StatusOK, map[string]any{
		"job_id": jobID,
		"recipe": recipe,
	})
}

// JobRecipeUpdate overwrites a job's VideoRecipe.
//
// The body takes the same shape as JobRecipe's response ({"recipe": {...}}). A bare VideoRecipe as
// the body is accepted too (an object with "cuts" can only be read as the recipe itself, so there
// is no mix-up).
//
// **It is allowed only for script-only jobs (StageScript).** Replacing the cut list after
// keyframes are rendered would make a stored keyframe_reference point at a different cut, quietly
// breaking the correspondence between pictures and script. To re-render a cut, use the regeneration path.
//
// Note: the durations saved here are re-rounded to Veo's supported durations by SceneSplitFilter at generation time.
func (h *Handler) JobRecipeUpdate(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if h.HistoryRepository == nil {
		respond.Error(w, r, http.StatusInternalServerError, "history storage adapter is not configured")
		return
	}

	current, err := h.HistoryRepository.GetRecipe(r.Context(), jobID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, "job not found")
		return
	}
	if stage := domain.NewJobProgress(current.Cuts).Stage; stage != domain.StageScript {
		respond.Error(w, r, http.StatusConflict,
			"recipe can only be edited before keyframes are generated (current stage: "+string(stage)+")")
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRecipeJSONSize))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, "failed to read request body")
		return
	}
	recipeJSON, err := recipeJSONFromBody(raw)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	recipe, err := domain.DecodeVideoRecipeJSON(recipeJSON)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid video recipe json: "+err.Error())
		return
	}

	if err := h.HistoryRepository.SaveRecipe(r.Context(), jobID, recipe); err != nil {
		slog.ErrorContext(r.Context(), "failed to save job recipe", "job_id", jobID, "error", err)
		// SaveRecipe can fail both validating the recipe and writing to GCS. Only a validation failure
		// (ErrRecipeInvalid) is the caller's mistake; returning 400 for a storage failure would be the lie
		// "your recipe is bad".
		status := http.StatusInternalServerError
		if errors.Is(err, video.ErrRecipeInvalid) {
			status = http.StatusBadRequest
		}
		respond.Error(w, r, status, err.Error())
		return
	}

	// Return the cut count and durations after saving. The point of editing is checking the cut list,
	// so there is no need to make the caller read the result back again.
	respond.JSON(w, r, http.StatusOK, map[string]any{
		"job_id":             jobID,
		"status":             "updated",
		"cut_count":          len(recipe.Cuts),
		"total_duration_sec": domain.TotalDurationSecOfCuts(recipe.Cuts),
	})
}

// recipeJSONFromBody extracts the VideoRecipe JSON from an update request body, accepting
// either {"recipe": {...}} (the GET response shape) or a bare VideoRecipe object.
func recipeJSONFromBody(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("request body is empty")
	}
	var envelope struct {
		Recipe json.RawMessage `json:"recipe"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, errors.New("invalid json body: " + err.Error())
	}
	if len(bytes.TrimSpace(envelope.Recipe)) > 0 {
		return envelope.Recipe, nil
	}
	return raw, nil
}
