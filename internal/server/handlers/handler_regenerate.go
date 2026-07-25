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

// loadHistoryForMutation loads history for jobID and validates it has a resolvable storage URI,
// as required by every handler that enqueues a follow-up task against an existing job. On
// failure it writes the appropriate error response itself and returns ok=false.
func (h *Handler) loadHistoryForMutation(w http.ResponseWriter, r *http.Request, jobID string) (domain.VideoHistoryDetail, bool) {
	if h.HistoryRepository == nil {
		writeError(w, r, http.StatusInternalServerError, "history storage adapter is not configured")
		return domain.VideoHistoryDetail{}, false
	}
	history, err := h.HistoryRepository.GetHistory(r.Context(), jobID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		return domain.VideoHistoryDetail{}, false
	}
	if strings.TrimSpace(history.StorageURI) == "" {
		writeError(w, r, http.StatusInternalServerError, "recipe storage URI is not available")
		return domain.VideoHistoryDetail{}, false
	}
	return history, true
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
	cut, ok := findHistoryCutByIndex(history.Cuts, cutIndex)
	if !ok {
		http.Error(w, "cut not found", http.StatusNotFound)
		return
	}
	seedDefault := ""
	if seed := h.CharacterOptions.seed(cut.CharacterID); seed != nil {
		seedDefault = strconv.FormatInt(*seed, 10)
	}
	h.renderPage(w, PageData{
		Title:                 "Regenerate Cut",
		CSRFToken:             csrfTokenFromContext(r.Context()),
		HistoryDetail:         history,
		RegenerateCut:         cut,
		RegenerateSeedDefault: seedDefault,
	}, "regenerate_cut.html")
}

// PostRegenerateCutKeyframe enqueues a keyframe regeneration task for a single cut.
func (h *Handler) PostRegenerateCutKeyframe(w http.ResponseWriter, r *http.Request) {
	jobID, cutIndex, err := parseJobIDAndCutIndex(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, ok := h.loadHistoryForMutation(w, r, jobID)
	if !ok {
		return
	}
	cut, ok := findHistoryCutByIndex(history.Cuts, cutIndex)
	if !ok {
		writeError(w, r, http.StatusNotFound, "cut not found")
		return
	}
	newJobID, err := jobid.New("regen-keyframe")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	task := &domain.Task{
		JobID:     newJobID,
		Command:   domain.CommandRegenerateCutKeyframe,
		RecipeURL: history.StorageURI,
		CutIndex:  &cutIndex,
		// 同一ジョブ内の他カットとアスペクト比がズレないよう、既存レシピの値を引き継ぐ。
		VeoAspectRatio:       history.AspectRatio,
		OverwriteKeyframe:    r.FormValue("overwrite") == "on",
		OriginalJobID:        jobID,
		VisualAnchorOverride: strings.TrimSpace(r.FormValue("visual_anchor")),
		EditPrompt:           strings.TrimSpace(r.FormValue("edit_prompt")),
		CreatedAt:            time.Now().UTC(),
	}
	if seedStr := strings.TrimSpace(r.FormValue("seed")); seedStr != "" {
		if strings.TrimSpace(cut.CharacterID) == "" {
			writeError(w, r, http.StatusBadRequest, "cut has no character to apply a seed override to")
			return
		}
		seed, err := strconv.ParseInt(seedStr, 10, 64)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid seed")
			return
		}
		if current := h.CharacterOptions.seed(cut.CharacterID); current == nil || *current != seed {
			task.SeedOverride = &seed
			task.SeedOverrideCharacterID = cut.CharacterID
		}
	}
	h.enqueue(w, r, task)
}

// PostRegenerateZip enqueues a ZIP re-creation task for an existing job.
// Optional form fields primary_color and secondary_color (CSS hex) override the default karaoke colors.
func (h *Handler) PostRegenerateZip(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, ok := h.loadHistoryForMutation(w, r, jobID)
	if !ok {
		return
	}
	newJobID, err := jobid.New("regen-zip")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
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

// PostGenerateVideoFromHistory は、既存ジョブの保存済みレシピから動画生成タスクを投入します。
// target=full で全カットのフルMV生成、target=<セクションインデックス> でそのセクションだけの
// ショート動画生成になります。キーフレーム・歌詞・時間割はジョブの保存済みレシピから
// サーバー側で解決するため、フォーム入力は対象と Veo モデル・アスペクト比だけです。
func (h *Handler) PostGenerateVideoFromHistory(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(chi.URLParam(r, "jobID"))
	if err := jobid.Validate(jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	history, ok := h.loadHistoryForMutation(w, r, jobID)
	if !ok {
		return
	}

	command, sectionIndex, err := resolveHistoryGenerationTask(strings.TrimSpace(r.FormValue("target")), len(history.Sections))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	task := &domain.Task{
		RecipeURL: history.StorageURI,
		VeoModel:  h.veoModelFromForm(r),
		// アスペクト比はキーフレーム作成時に1回だけ選ばれ、既存レシピに記録済みの値
		// (history.AspectRatio) を必ず引き継ぐ。動画生成フォームでの選択肢は無い
		// （キーフレームと異なるアスペクト比の動画は生成できないため）。
		VeoAspectRatio: history.AspectRatio,
		Command:        command,
		SectionIndex:   sectionIndex,
		CreatedAt:      time.Now().UTC(),
	}
	jobIDPrefix := "mv"
	if command == domain.CommandShortVideoFromSection {
		jobIDPrefix = "short"
	}
	task.JobID, err = jobid.New(jobIDPrefix)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
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
