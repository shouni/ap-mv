package repository

import (
	"strings"

	"github.com/shouni/ap-mv/internal/domain"
)

// This file gathers the conversion from job status and VideoRecipe into history display models.
// For storage and signed-URL access see history_storage.go; for the public entry points see
// history_listing.go.

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

// videoHistoryFromStatus builds one listing row from a job status document.
//
// Storage locations (metadata, ZIP) and the creation time can be derived from the job ID, so they
// are not copied into the document. Copying derivable values would leave old rows pointing at old
// paths when the location changes.
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

// videoHistoryFromRecipe builds one detail's headline from a stored recipe.
//
// domain.JobStatus.ApplyVideoRecipe is the single definition of copying a recipe into the
// headline; this goes through it and then through the same conversion as the listing. The
// listing and detail used to each map the recipe themselves, and since adding a field to one side
// still compiles, fields appeared only on the detail. Now that the listing headline is a copy in
// the status document, such a mismatch would show on the page.
//
// State and Error cannot be told from the artifacts, so they stay empty here (GetHistory fills
// them in from the status document).
func (r *VideoHistoryRepository) videoHistoryFromRecipe(jobID string, recipe domain.VideoRecipe) domain.VideoHistory {
	var status domain.JobStatus
	status.JobID = jobID
	status.ApplyVideoRecipe(&recipe)
	return r.videoHistoryFromStatus(status)
}
