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

// GenerateSectionVideo は、指定セクションのカットだけを「キーフレーム → 動画」の順に生成し、
// 結果を**元のジョブへ書き戻す**タスクを投入します。
//
// 履歴詳細の「動画生成（target=セクション）」との違いは出力先です。あちらは 60 秒に収めた
// 独立したショート動画を新しいジョブとして作りますが、こちらは 1 本の MV を 1 セクションずつ
// 積み上げる操作で、同じジョブのレシピが少しずつ埋まっていきます。台本だけのジョブから
// セクション 1 で画作りを確かめ、良ければ次へ進む、という進め方ができます。
//
// 結合（final_video_url の作成）は行いません。セクションを 1 つ焼くたびに結合し直すと、
// 完成品と途中経過が同じ URL で見分けられなくなるためです。全セクションが揃ったら
// Finalize で 1 本にまとめます。
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
	// 投入前に確認しておく。ここで弾かないと、何も生成しないまま失敗したジョブとしてしか現れない。
	if len(group.Cuts) == 0 {
		respond.Error(w, r, http.StatusBadRequest, "section has no cuts to generate")
		return
	}
	// タスク自身の JobID は Cloud Tasks のタスク名を衝突させないため新規に採番します。
	// 成果物の保存先は OriginalJobOutputFilter が元ジョブへ戻すため、ここでの採番は
	// キューの都合だけで、GCS 上に新しいジョブのディレクトリが生えることはありません。
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
		// アスペクト比はキーフレーム作成時に1回だけ決まった値を必ず引き継ぎます
		// （RegenerateCutVideo と同じ理由）。
		VeoAspectRatio: history.AspectRatio,
		OriginalJobID:  jobID,
		CreatedAt:      time.Now().UTC(),
	}
	h.enqueue(w, r, task)
}

// Finalize は、生成済みカットの動画を1本の完成動画へ結合するタスクを投入します。
// 生成は行わないため追加の課金は発生せず、section_video で積み上げた結果の仕上げに使います。
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
