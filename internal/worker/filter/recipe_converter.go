package filter

import (
	"strings"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"ap-mv/internal/domain"
)

// toVideoRecipe converts a domain music recipe to an orchestrator video recipe.
func toVideoRecipe(recipe *domain.MusicRecipe) (*orchestrator.VideoRecipe, error) {
	if recipe == nil {
		return nil, nil
	}
	if err := domain.NormalizeMusicRecipe(recipe); err != nil {
		return nil, err
	}

	musicRecipe := *recipe
	musicRecipe.Sections = append([]domain.MusicSection(nil), recipe.Sections...)
	musicRecipe.Instruments = append([]string(nil), recipe.Instruments...)
	videoRecipe := &orchestrator.VideoRecipe{
		ProjectTitle: recipe.Title,
		Title:        recipe.Title,
		Theme:        recipe.Theme,
		Mood:         recipe.Mood,
		Tempo:        recipe.Tempo,
		Instruments:  append([]string(nil), recipe.Instruments...),
		AudioModel:   recipe.TextModel,
		Seed:         seedValue(recipe.Seed),
		MusicRecipe:  musicRecipe,
		Sections:     make([]orchestrator.Section, 0, len(recipe.Sections)),
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
			Name:         section.Name,
			Duration:     section.Duration,
			StartSeconds: section.StartSeconds,
			EndSeconds:   section.EndSeconds,
			Prompt:       section.Prompt,
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
	if task.Command == domain.CommandVideoRecipeCreate {
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
		Mood:        nonEmpty(recipe.Mood, recipe.MusicRecipe.Mood),
		Tempo:       firstPositiveInt(recipe.Tempo, recipe.MusicRecipe.Tempo),
		Instruments: append([]string(nil), recipe.Instruments...),
		Sections:    make([]domain.MusicSection, 0, len(recipe.Sections)),
	}
	domainRecipe.AIModels = recipe.MusicRecipe.AIModels
	if domainRecipe.TextModel == "" {
		domainRecipe.TextModel = recipe.AudioModel
	}
	if domainRecipe.Seed == nil {
		domainRecipe.Seed = seedPtr(recipe.Seed)
	}
	if len(domainRecipe.Instruments) == 0 {
		domainRecipe.Instruments = append([]string(nil), recipe.MusicRecipe.Instruments...)
	}
	if len(recipe.MusicRecipe.Sections) > 0 && len(recipe.Sections) == 0 {
		domainRecipe.Sections = append([]domain.MusicSection(nil), recipe.MusicRecipe.Sections...)
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
			Name:         section.Name,
			Duration:     section.Duration,
			StartSeconds: section.StartSeconds,
			EndSeconds:   section.EndSeconds,
			Prompt:       section.Prompt,
		})
	}
	if len(domainRecipe.Sections) == 0 {
		domainRecipe.Sections = []domain.MusicSection{{
			Name:     "main",
			Duration: 8,
			Prompt:   domainRecipe.Theme,
		}}
	}
	return domainRecipe, domain.NormalizeMusicRecipe(domainRecipe)
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
