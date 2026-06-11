package filter

import (
	"math"
	"strings"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"ap-mv/internal/domain"
)

// toVideoRecipe converts a domain music recipe to an orchestrator video recipe.
func toVideoRecipe(recipe *domain.MusicRecipe) (*orchestrator.VideoRecipe, error) {
	if recipe == nil {
		return nil, nil
	}
	if err := recipe.Normalize(); err != nil {
		return nil, err
	}

	videoRecipe := &orchestrator.VideoRecipe{
		ProjectTitle: recipe.Title,
		Title:        recipe.Title,
		Theme:        recipe.Theme,
		Mood:         recipe.Mood,
		Tempo:        recipe.Tempo,
		Instruments:  append([]string(nil), recipe.Instruments...),
		AudioModel:   recipe.TextModel,
		Seed:         seedValue(recipe.Seed),
		MusicRecipe: orchestrator.MusicRecipe{
			TempoBPM: recipe.Tempo,
			Style:    recipe.Mood,
		},
		Sections: make([]orchestrator.Section, 0, len(recipe.Sections)),
		Cuts:     make([]orchestrator.Cut, 0, len(recipe.Cuts)),
	}
	if recipe.Lyrics != nil {
		videoRecipe.Lyrics = &orchestrator.Lyrics{
			Title:     recipe.Lyrics.Title,
			Theme:     recipe.Lyrics.Theme,
			Hook:      recipe.Lyrics.Hook,
			Lyrics:    recipe.Lyrics.Lyrics,
			Keywords:  append([]string(nil), recipe.Lyrics.Keywords...),
			Mood:      recipe.Lyrics.Mood,
			Narrative: recipe.Lyrics.Narrative,
		}
	}
	for _, section := range recipe.Sections {
		videoRecipe.Sections = append(videoRecipe.Sections, orchestrator.Section{
			Name:            section.Name,
			DurationSeconds: float64(section.Duration),
			Prompt:          section.Prompt,
		})
	}
	for _, cut := range recipe.Cuts {
		videoRecipe.Cuts = append(videoRecipe.Cuts, orchestrator.Cut{
			CutIndex:          cut.Index + 1,
			DurationSec:       float64(cut.DurationSec),
			AudioCue:          nonEmpty(cut.AudioCue, cut.Prompt),
			AudioReference:    cut.AudioURI,
			VisualAnchor:      nonEmpty(cut.Prompt, cut.SectionName),
			CharacterID:       strings.TrimSpace(cut.ImageRefName),
			KeyframeReference: cut.KeyframeURI,
			VideoURL:          cut.VideoURL,
			VideoID:           cut.VideoID,
			Status:            toOrchestratorStatus(cut.Status),
			StartSec:          float64(cut.StartSec),
			EndSec:            float64(cut.EndSec),
		})
	}
	videoRecipe.Normalize()
	return videoRecipe, nil
}

// applyTaskAudioURLToVideoRecipe applies a task audio URL to an orchestrator recipe.
func applyTaskAudioURLToVideoRecipe(task *domain.Task, recipe *orchestrator.VideoRecipe) {
	if task == nil || recipe == nil {
		return
	}
	audioURL := strings.TrimSpace(task.AudioURL)
	if audioURL == "" {
		return
	}
	recipe.Normalize()
	for i := range recipe.Cuts {
		if strings.TrimSpace(recipe.Cuts[i].AudioReference) == "" {
			recipe.Cuts[i].AudioReference = audioURL
		}
	}
}

// applyTaskCharacterIDToVideoRecipe applies a task character ID to an orchestrator recipe.
func applyTaskCharacterIDToVideoRecipe(task *domain.Task, recipe *orchestrator.VideoRecipe) {
	if task == nil || recipe == nil {
		return
	}
	characterID := strings.TrimSpace(task.CharacterID)
	if characterID == "" {
		return
	}
	recipe.Normalize()
	for i := range recipe.Cuts {
		recipe.Cuts[i].CharacterID = characterID
	}
}

// toDomainRecipe converts an orchestrator video recipe to a domain music recipe.
func toDomainRecipe(recipe *orchestrator.VideoRecipe) (*domain.MusicRecipe, error) {
	if recipe == nil {
		return nil, nil
	}
	recipe.Normalize()

	domainRecipe := &domain.MusicRecipe{
		Title:       nonEmpty(recipe.Title, recipe.ProjectTitle),
		Theme:       recipe.Theme,
		Mood:        nonEmpty(recipe.Mood, recipe.MusicRecipe.Style),
		Tempo:       firstPositiveInt(recipe.Tempo, recipe.MusicRecipe.TempoBPM),
		Instruments: append([]string(nil), recipe.Instruments...),
		Sections:    make([]domain.MusicSection, 0, len(recipe.Sections)),
		Cuts:        make([]domain.VideoCut, 0, len(recipe.Cuts)),
		AIModels: domain.AIModels{
			TextModel:  recipe.AudioModel,
			ImageModel: "",
			Seed:       seedPtr(recipe.Seed),
		},
	}
	if recipe.Lyrics != nil {
		domainRecipe.Lyrics = &domain.LyricsDraft{
			Title:     recipe.Lyrics.Title,
			Theme:     recipe.Lyrics.Theme,
			Hook:      recipe.Lyrics.Hook,
			Lyrics:    recipe.Lyrics.Lyrics,
			Keywords:  append([]string(nil), recipe.Lyrics.Keywords...),
			Mood:      recipe.Lyrics.Mood,
			Narrative: recipe.Lyrics.Narrative,
		}
	}
	for _, section := range recipe.Sections {
		domainRecipe.Sections = append(domainRecipe.Sections, domain.MusicSection{
			Name:     section.Name,
			Duration: int(math.Round(section.DurationSeconds)),
			Prompt:   section.Prompt,
		})
	}
	for _, cut := range recipe.Cuts {
		index := cut.CutIndex - 1
		if index < 0 {
			index = 0
		}
		domainRecipe.Cuts = append(domainRecipe.Cuts, domain.VideoCut{
			Index:        index,
			SectionName:  cut.VisualAnchor,
			StartSec:     int(math.Round(cut.StartSec)),
			EndSec:       int(math.Round(cut.EndSec)),
			DurationSec:  int(math.Round(cut.DurationSec)),
			Prompt:       cut.VisualAnchor,
			AudioCue:     cut.AudioCue,
			Status:       toDomainStatus(cut.Status),
			VideoID:      cut.VideoID,
			VideoURL:     cut.VideoURL,
			KeyframeURI:  cut.KeyframeReference,
			AudioURI:     cut.AudioReference,
			ImageRefName: cut.CharacterID,
		})
	}
	if len(domainRecipe.Sections) == 0 && len(domainRecipe.Cuts) == 0 {
		domainRecipe.Sections = []domain.MusicSection{{
			Name:     "main",
			Duration: 8,
			Prompt:   domainRecipe.Theme,
		}}
	}
	return domainRecipe, domainRecipe.Normalize()
}

// toOrchestratorStatus converts values to orchestrator status.
func toOrchestratorStatus(status string) orchestrator.CutStatus {
	switch status {
	case domain.CutStatusGenerated:
		return orchestrator.CutStatusGenerated
	case string(orchestrator.CutStatusFailed):
		return orchestrator.CutStatusFailed
	default:
		return orchestrator.CutStatusPending
	}
}

// toDomainStatus converts values to domain status.
func toDomainStatus(status orchestrator.CutStatus) string {
	if status == orchestrator.CutStatusGenerated {
		return domain.CutStatusGenerated
	}
	return string(status)
}

// seedValue returns zero for nil seeds or the seed value otherwise.
func seedValue(seed *int64) int64 {
	if seed == nil {
		return 0
	}
	return *seed
}

// seedPtr returns nil for zero seeds or a pointer to the seed otherwise.
func seedPtr(seed int64) *int64 {
	if seed == 0 {
		return nil
	}
	return &seed
}

// firstPositiveInt returns the first matching positive int.
func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

// nonEmpty returns the first non-empty string.
func nonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
