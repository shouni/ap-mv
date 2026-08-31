package repository

import (
	"strings"

	"github.com/shouni/ap-mv/internal/domain"
)

// このファイルは VideoRecipe から履歴表示用モデルへの純粋な変換を集めています。
// ストレージ・キャッシュ・署名URLへのアクセスは history_storage.go、
// 公開エントリポイントは history_listing.go を参照してください。

// historySectionsFromRecipe converts recipe sections into display-ready entries with
// normalized time ranges, so the short-video form can label each section option.
func historySectionsFromRecipe(recipe domain.VideoRecipe) []domain.VideoHistorySection {
	sections := recipe.MusicRecipe.Sections
	if len(sections) == 0 {
		return nil
	}
	result := make([]domain.VideoHistorySection, 0, len(sections))
	cursor := 0
	for i, sec := range sections {
		start := sec.StartSeconds
		if start == 0 && i > 0 {
			start = cursor
		}
		end := domain.SectionEndSeconds(start, sec.EndSeconds, sec.Duration)
		cursor = end
		result = append(result, domain.VideoHistorySection{
			SectionIndex: i,
			Name:         strings.TrimSpace(sec.Name),
			StartSeconds: start,
			EndSeconds:   end,
			Generated:    sectionCutsAllGenerated(recipe.Cuts, float64(start), float64(end)),
		})
	}
	return result
}

// sectionCutsAllGenerated reports whether every cut whose StartSec falls within [start, end)
// already has status=generated. A section with no matching cuts is not considered generated,
// since there is nothing there to skip.
func sectionCutsAllGenerated(cuts []domain.VideoCut, start, end float64) bool {
	found := false
	for _, cut := range cuts {
		if cut.StartSec < start || cut.StartSec >= end {
			continue
		}
		found = true
		if !cut.IsGenerated() {
			return false
		}
	}
	return found
}

// videoHistoryFromStatus は、ジョブ状態のドキュメントから一覧 1 行分を組み立てます。
//
// 保存先（メタデータ・ZIP）と作成日時はジョブ ID から導けるので、ドキュメントには
// 写していません。導ける値を写すと、置き場を変えたときに古い行だけが古いパスを
// 指したまま残ります。
func (r *VideoHistoryRepository) videoHistoryFromStatus(status domain.JobStatus) domain.VideoHistory {
	jobID := strings.TrimSpace(status.JobID)
	title := strings.TrimSpace(status.Title)
	if title == "" {
		title = jobID
	}
	return domain.VideoHistory{
		JobID:            jobID,
		Title:            title,
		State:            status.State,
		Error:            strings.TrimSpace(status.Error),
		Mood:             strings.TrimSpace(status.Mood),
		Tempo:            status.Tempo,
		CreatedAt:        formatHistoryCreatedAt(jobID),
		VisualMode:       strings.TrimSpace(status.VisualMode),
		CutCount:         status.Progress.TotalCuts,
		StorageURI:       r.metadataURI(jobID),
		Generated:        status.Progress.IsCompleted(),
		Progress:         status.Progress,
		KeyframeZipURI:   r.keyframeZipURI(jobID),
		FinalVideoURL:    strings.TrimSpace(status.FinalVideoURL),
		AspectRatio:      strings.TrimSpace(status.AspectRatio),
		GeneratedSeconds: status.GeneratedSeconds,
	}
}

// videoHistoryFromRecipe は、保存済みレシピから詳細 1 件分の見出しを組み立てます。
//
// レシピから見出しへの写しは domain.JobStatus.ApplyVideoRecipe が唯一の定義元で、ここは
// それを通してから一覧と同じ変換にかけます。以前は一覧と詳細がそれぞれレシピを読み替えて
// おり、片方に項目を足してもコンパイルは通るため、詳細にだけ出る項目が生まれていました。
// 一覧の見出しが状態ドキュメントの写しになった今は、その食い違いが画面に出ます。
//
// State と Error は成果物からは分からないので、ここでは空のままです（GetHistory が
// 状態ドキュメントから補います）。
func (r *VideoHistoryRepository) videoHistoryFromRecipe(jobID string, recipe domain.VideoRecipe) domain.VideoHistory {
	var status domain.JobStatus
	status.JobID = jobID
	status.ApplyVideoRecipe(&recipe)
	return r.videoHistoryFromStatus(status)
}
