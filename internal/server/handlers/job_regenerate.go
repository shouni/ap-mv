package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"

	"github.com/shouni/go-serve-kit/respond"
)

// findHistoryCutByIndex returns the cut matching cutIndex, or false if none matches.
func findHistoryCutByIndex(cuts []domain.VideoHistoryCut, cutIndex int) (domain.VideoHistoryCut, bool) {
	for _, cut := range cuts {
		if cut.CutIndex == cutIndex {
			return cut, true
		}
	}
	return domain.VideoHistoryCut{}, false
}

// parseJobIDAndCutIndex reads and validates the jobID and cutIndex URL params shared by the
// regenerate-keyframe form page and its submit handler.
func parseJobIDAndCutIndex(r *http.Request) (jobID string, cutIndex int, err error) {
	jobID = strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err = jobid.Validate(jobID); err != nil {
		return "", 0, err
	}
	cutIndexStr := strings.TrimSpace(chi.URLParam(r, "cutIndex"))
	cutIndex, err = strconv.Atoi(cutIndexStr)
	if err != nil || cutIndex < 1 {
		return "", 0, errors.New("invalid cut_index")
	}
	return jobID, cutIndex, nil
}

// parseJobIDAndSectionIndex reads and validates the jobID and sectionIndex URL params shared by
// the section regenerate form page and its submit handler. sectionIndex is the 0-based index into
// the recipe's section list, matching domain.Task.SectionIndex.
func parseJobIDAndSectionIndex(r *http.Request) (jobID string, sectionIndex int, err error) {
	jobID = strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err = jobid.Validate(jobID); err != nil {
		return "", 0, err
	}
	sectionIndex, err = strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "sectionIndex")))
	if err != nil || sectionIndex < 0 {
		return "", 0, errors.New("invalid section_index")
	}
	return jobID, sectionIndex, nil
}

// findHistorySectionGroup returns the section and its cuts for sectionIndex, or false when the
// recipe has no such section. SectionGroups() aligns with the recipe's section order (plus a
// trailing group for cuts outside every section), so indexing is only valid within Sections.
func findHistorySectionGroup(history domain.VideoHistoryDetail, sectionIndex int) (domain.VideoHistorySectionGroup, bool) {
	if sectionIndex < 0 || sectionIndex >= len(history.Sections) {
		return domain.VideoHistorySectionGroup{}, false
	}
	groups := history.SectionGroups()
	if sectionIndex >= len(groups) {
		return domain.VideoHistorySectionGroup{}, false
	}
	return groups[sectionIndex], true
}

// applySeedOverride reads the optional "seed" form field and records it on the task as a
// temporary override for characterID. It reports ok=false after writing an error response when
// the request is unusable. A seed equal to the character's configured value is left off the task,
// since overriding with the current value would be a no-op.
func (h *Handler) applySeedOverride(w http.ResponseWriter, r *http.Request, task *domain.Task, characterID string) bool {
	seedStr := strings.TrimSpace(r.FormValue("seed"))
	if seedStr == "" {
		return true
	}
	characterID = strings.TrimSpace(characterID)
	if characterID == "" {
		respond.Error(w, r, http.StatusBadRequest, "no character to apply a seed override to")
		return false
	}
	seed, err := strconv.ParseInt(seedStr, 10, 64)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid seed")
		return false
	}
	if current := h.CharacterOptions.seed(characterID); current == nil || *current != seed {
		task.SeedOverride = &seed
		task.SeedOverrideCharacterID = characterID
	}
	return true
}

// loadHistoryForMutation loads history for jobID and validates it has a resolvable storage URI,
// as required by every handler that enqueues a follow-up task against an existing job. On
// failure it writes the appropriate error response itself and returns ok=false.
func (h *Handler) loadHistoryForMutation(w http.ResponseWriter, r *http.Request, jobID string) (domain.VideoHistoryDetail, bool) {
	if h.HistoryRepository == nil {
		respond.Error(w, r, http.StatusInternalServerError, "history storage adapter is not configured")
		return domain.VideoHistoryDetail{}, false
	}
	history, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		respond.ServerError(w, r, http.StatusInternalServerError, err, "failed to get history for regeneration", "job_id", jobID)
		return domain.VideoHistoryDetail{}, false
	}
	if strings.TrimSpace(history.StorageURI) == "" {
		respond.Error(w, r, http.StatusInternalServerError, "recipe storage URI is not available")
		return domain.VideoHistoryDetail{}, false
	}
	return history, true
}

// mintJobID issues a new job ID for a task, writing the error response itself when minting
// fails. It follows loadHistoryForMutation's shape — the caller checks ok and returns — because
// the same five-line block was repeated at every enqueue site in this package.
func mintJobID(w http.ResponseWriter, r *http.Request, prefix string) (string, bool) {
	jobID, err := jobid.New(prefix)
	if err != nil {
		respond.ServerError(w, r, http.StatusInternalServerError, err, "failed to mint a job ID", "prefix", prefix)
		return "", false
	}
	return jobID, true
}

// RegenerateCutKeyframeForm renders a dedicated page for configuring a single cut's
// keyframe regeneration (prompt override, seed override, overwrite) before submitting it.
func (h *Handler) RegenerateCutKeyframeForm(w http.ResponseWriter, r *http.Request) {
	jobID, cutIndex, err := parseJobIDAndCutIndex(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.HistoryRepository == nil {
		http.Error(w, "history storage adapter is not configured", http.StatusInternalServerError)
		return
	}
	history, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to get history detail for regenerate form",
			"job_id", jobID,
			"error", err,
		)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	// The page points at same-origin paths (exactly one URL is signed, at the 302).
	applyWebMediaURLs(&history)
	cut, ok := findHistoryCutByIndex(history.Cuts, cutIndex)
	if !ok {
		http.Error(w, "cut not found", http.StatusNotFound)
		return
	}
	seedDefault := ""
	if seed := h.CharacterOptions.seed(cut.CharacterID); seed != nil {
		seedDefault = strconv.FormatInt(*seed, 10)
	}
	h.renderPage(w, r, PageData{
		Title:                 "Regenerate Cut",
		CSRFToken:             csrfTokenFromContext(r.Context()),
		HistoryDetail:         history,
		RegenerateCut:         cut,
		RegenerateSeedDefault: seedDefault,
	}, "regenerate_cut.html")
}

// RegenerateCutKeyframe enqueues a keyframe regeneration task for a single cut.
func (h *Handler) RegenerateCutKeyframe(w http.ResponseWriter, r *http.Request) {
	jobID, cutIndex, err := parseJobIDAndCutIndex(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, ok := h.loadHistoryForMutation(w, r, jobID)
	if !ok {
		return
	}
	cut, ok := findHistoryCutByIndex(history.Cuts, cutIndex)
	if !ok {
		respond.Error(w, r, http.StatusNotFound, "cut not found")
		return
	}
	newJobID, ok := mintJobID(w, r, "regen-keyframe")
	if !ok {
		return
	}
	task := &domain.Task{
		JobID:     newJobID,
		Command:   domain.CommandRegenerateCutKeyframe,
		RecipeURL: history.StorageURI,
		CutIndex:  &cutIndex,
		// Carry over the existing recipe's value so the aspect ratio does not drift from the job's other cuts.
		VeoAspectRatio:       history.AspectRatio,
		OverwriteKeyframe:    r.FormValue("overwrite") == "on",
		OriginalJobID:        jobID,
		VisualAnchorOverride: strings.TrimSpace(r.FormValue("visual_anchor")),
		EditPrompt:           strings.TrimSpace(r.FormValue("edit_prompt")),
		CreatedAt:            time.Now().UTC(),
	}
	if !h.applySeedOverride(w, r, task, cut.CharacterID) {
		return
	}
	h.enqueue(w, r, task)
}

// RegenerateSectionKeyframesForm renders a page for regenerating every keyframe of one music
// section at once (full regenerate or local edit), showing the section's current keyframes.
func (h *Handler) RegenerateSectionKeyframesForm(w http.ResponseWriter, r *http.Request) {
	jobID, sectionIndex, err := parseJobIDAndSectionIndex(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.HistoryRepository == nil {
		http.Error(w, "history storage adapter is not configured", http.StatusInternalServerError)
		return
	}
	history, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to get history detail for section regenerate form",
			"job_id", jobID,
			"error", err,
		)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	// The page points at same-origin paths (exactly one URL is signed, at the 302).
	applyWebMediaURLs(&history)
	group, ok := findHistorySectionGroup(history, sectionIndex)
	if !ok {
		http.Error(w, "section not found", http.StatusNotFound)
		return
	}
	seedDefault := ""
	if seed := h.CharacterOptions.seed(sectionCharacterID(group)); seed != nil {
		seedDefault = strconv.FormatInt(*seed, 10)
	}
	h.renderPage(w, r, PageData{
		Title:                 "Regenerate Section",
		CSRFToken:             csrfTokenFromContext(r.Context()),
		HistoryDetail:         history,
		RegenerateSection:     group,
		RegenerateSeedDefault: seedDefault,
	}, "regenerate_section.html")
}

// RegenerateSectionKeyframes enqueues a keyframe regeneration task for every cut of one section.
func (h *Handler) RegenerateSectionKeyframes(w http.ResponseWriter, r *http.Request) {
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
	if len(group.Cuts) == 0 {
		respond.Error(w, r, http.StatusBadRequest, "section has no cuts to regenerate")
		return
	}
	newJobID, ok := mintJobID(w, r, "regen-section")
	if !ok {
		return
	}
	task := &domain.Task{
		JobID:        newJobID,
		Command:      domain.CommandRegenerateSectionKeyframes,
		RecipeURL:    history.StorageURI,
		SectionIndex: &sectionIndex,
		// Carry over the existing recipe's value so the aspect ratio does not drift from the job's other cuts.
		VeoAspectRatio:    history.AspectRatio,
		OverwriteKeyframe: r.FormValue("overwrite") == "on",
		OriginalJobID:     jobID,
		// The visual anchor's wording differs per cut, so it is not accepted section-wide
		// (a full regeneration uses each cut's stored anchor as-is).
		EditPrompt: strings.TrimSpace(r.FormValue("edit_prompt")),
		CreatedAt:  time.Now().UTC(),
	}
	if !h.applySeedOverride(w, r, task, sectionCharacterID(group)) {
		return
	}
	h.enqueue(w, r, task)
}

// sectionCharacterID returns the character the section's cuts belong to, taken from its first cut
// with one set. Sections are normally single-character, and a seed override targets one character
// by ID, so cuts of any other character in the section are simply left untouched by it.
func sectionCharacterID(group domain.VideoHistorySectionGroup) string {
	for _, cut := range group.Cuts {
		if id := strings.TrimSpace(cut.CharacterID); id != "" {
			return id
		}
	}
	return ""
}

// RegenerateZip enqueues a ZIP re-creation task for an existing job.
// Optional form fields primary_color and secondary_color (CSS hex) override the default karaoke colors.
func (h *Handler) RegenerateZip(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, ok := h.loadHistoryForMutation(w, r, jobID)
	if !ok {
		return
	}
	newJobID, ok := mintJobID(w, r, "regen-zip")
	if !ok {
		return
	}
	task := &domain.Task{
		JobID:             newJobID,
		Command:           domain.CommandRegenerateZip,
		RecipeURL:         history.StorageURI,
		ASSPrimaryColor:   strings.TrimSpace(r.FormValue("primary_color")),
		ASSSecondaryColor: strings.TrimSpace(r.FormValue("secondary_color")),
		OriginalJobID:     jobID,
		CreatedAt:         time.Now().UTC(),
	}
	h.enqueue(w, r, task)
}

// RegenerateCutVideo enqueues a task that remakes only the given cut's video of an existing job.
//
// The keyframes are the original job's as-is, and cuts not being remade are skipped as generated.
// In the continuation-chain approach, later cuts of the same chain are remade too
// (CutVideoSelectFilter). The result is saved as a new job and the original job is not changed
// (the same as the history detail's video generation, so a finished MV is never left half broken).
func (h *Handler) RegenerateCutVideo(w http.ResponseWriter, r *http.Request) {
	jobID, cutIndex, err := parseJobIDAndCutIndex(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, ok := h.loadHistoryForMutation(w, r, jobID)
	if !ok {
		return
	}
	// Check existence before enqueueing. Without rejecting here, it would only show up as a failed job.
	if _, found := findHistoryCutByIndex(history.Cuts, cutIndex); !found {
		respond.Error(w, r, http.StatusBadRequest, "cut not found in this job")
		return
	}
	newJobID, ok := mintJobID(w, r, "regen-video")
	if !ok {
		return
	}
	task := &domain.Task{
		JobID:     newJobID,
		Command:   domain.CommandRegenerateCutVideo,
		RecipeURL: history.StorageURI,
		CutIndex:  &cutIndex,
		VeoModel:  h.veoModelFromForm(r),
		// The aspect ratio always carries over the value decided when the keyframes were made
		// (the same reason as GenerateVideo).
		VeoAspectRatio: history.AspectRatio,
		CreatedAt:      time.Now().UTC(),
	}
	h.enqueue(w, r, task)
}

// GenerateVideo enqueues a video-generation task from an existing job's stored recipe.
// target=full makes a full MV of every cut; target=<section index> makes a short of that section
// only. Keyframes, lyrics and timing are resolved on the server from the job's stored recipe, so
// the form only gives the target, the Veo model and the aspect ratio.
func (h *Handler) GenerateVideo(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, ok := h.loadHistoryForMutation(w, r, jobID)
	if !ok {
		return
	}

	command, sectionIndex, err := resolveHistoryGenerationTask(strings.TrimSpace(r.FormValue("target")), len(history.Sections))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}
	task := &domain.Task{
		RecipeURL: history.StorageURI,
		VeoModel:  h.veoModelFromForm(r),
		// The aspect ratio is chosen once when the keyframes are made, and the value recorded in the
		// existing recipe (history.AspectRatio) is always carried over. The video-generation form has no
		// choice for it (a video with a different aspect ratio from its keyframes cannot be generated).
		VeoAspectRatio: history.AspectRatio,
		Command:        command,
		SectionIndex:   sectionIndex,
		CreatedAt:      time.Now().UTC(),
	}
	jobIDPrefix := "mv"
	if command == domain.CommandShortVideoFromSection {
		jobIDPrefix = "short"
	}
	newJobID, ok := mintJobID(w, r, jobIDPrefix)
	if !ok {
		return
	}
	task.JobID = newJobID
	h.enqueue(w, r, task)
}

// resolveHistoryGenerationTask decides the task command and section index implied by a
// "generate video from history" request's target selector: "full" for the whole recipe's MV,
// or a section array index (as a string) for a short video from just that section.
func resolveHistoryGenerationTask(target string, sectionCount int) (domain.TaskCommand, *int, error) {
	if target == "full" {
		return domain.CommandMVFromKeyframeVideoRecipe, nil, nil
	}
	if sectionCount == 0 {
		return "", nil, errors.New("no sections available in this recipe")
	}
	sectionIndex, err := strconv.Atoi(target)
	if err != nil || sectionIndex < 0 || sectionIndex >= sectionCount {
		return "", nil, errors.New("invalid target section")
	}
	return domain.CommandShortVideoFromSection, &sectionIndex, nil
}
