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

func videoHistoryFromRecipe(jobID string, metadataURI string, recipe domain.VideoRecipe) domain.VideoHistory {
	history := domain.VideoHistory{
		JobID:         jobID,
		Title:         strings.TrimSpace(firstNonEmpty(recipe.MusicRecipe.Title, recipe.ProjectTitle)),
		Mood:          strings.TrimSpace(recipe.MusicRecipe.Mood),
		Tempo:         recipe.MusicRecipe.Tempo,
		CreatedAt:     formatHistoryCreatedAt(jobID),
		VisualMode:    strings.TrimSpace(recipe.MusicRecipe.ComposeMode),
		CutCount:      len(recipe.Cuts),
		StorageURI:    metadataURI,
		Generated:     allCutsGenerated(recipe.Cuts),
		FinalVideoURL: strings.TrimSpace(recipe.FinalVideoURL),
		AspectRatio:   strings.TrimSpace(recipe.AspectRatio),
	}
	if history.Title == "" {
		history.Title = jobID
	}
	return history
}

func allCutsGenerated(cuts []domain.VideoCut) bool {
	if len(cuts) == 0 {
		return false
	}
	for _, cut := range cuts {
		if !cut.IsGenerated() {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
