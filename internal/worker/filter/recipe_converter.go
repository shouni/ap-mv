package filter

import (
	"fmt"
	"path"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"
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
		MusicRecipe:  musicRecipe,
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

// originalJobOutputPath は RecipeURL（例: gs://bucket/jobs/{jobID}/video_music_meta.json）から
// ジョブルートパス（例: gs://bucket/jobs/{jobID}/）を導出します。
// path.Dir は gs:// の // を潰すため remoteio でスキームを分解してから操作します。
func originalJobOutputPath(recipeURL string) string {
	recipeURL = strings.TrimSpace(recipeURL)
	if recipeURL == "" {
		return ""
	}
	bucket, objPath, err := remoteio.ParseRemoteURI(recipeURL)
	if err != nil {
		return ""
	}
	dir := path.Dir(objPath)
	if dir == "." || dir == "" || dir == "/" {
		return ""
	}
	return remoteio.BuildGCSURI(bucket, dir) + "/"
}

// findCutByIndex はレシピ内の指定 cutIndex に対応するスライスインデックスを返します。
func findCutByIndex(cuts []orchestrator.Cut, cutIndex int) (int, error) {
	for i := range cuts {
		if cuts[i].CutIndex == cutIndex {
			return i, nil
		}
	}
	return -1, fmt.Errorf("cut index %d not found in recipe", cutIndex)
}

// toDomainRecipe converts an orchestrator video recipe to a domain music recipe.
func toDomainRecipe(recipe *orchestrator.VideoRecipe) (*domain.MusicRecipe, error) {
	if recipe == nil {
		return nil, nil
	}
	recipe.Normalize()

	domainRecipe := &domain.MusicRecipe{
		Title:       nonEmpty(recipe.MusicRecipe.Title, recipe.ProjectTitle),
		Theme:       recipe.MusicRecipe.Theme,
		Mood:        recipe.MusicRecipe.Mood,
		Tempo:       recipe.MusicRecipe.Tempo,
		Instruments: append([]string(nil), recipe.MusicRecipe.Instruments...),
		Sections:    make([]domain.MusicSection, 0, len(recipe.MusicRecipe.Sections)),
	}
	domainRecipe.AIModels = recipe.MusicRecipe.AIModels
	if len(recipe.MusicRecipe.Sections) > 0 {
		domainRecipe.Sections = append([]domain.MusicSection(nil), recipe.MusicRecipe.Sections...)
	}
	if recipe.MusicRecipe.Lyrics != nil {
		domainRecipe.Lyrics = &domain.LyricsDraft{
			Title:     recipe.MusicRecipe.Lyrics.Title,
			Theme:     recipe.MusicRecipe.Lyrics.Theme,
			Hook:      recipe.MusicRecipe.Lyrics.Hook,
			Lyrics:    recipe.MusicRecipe.Lyrics.Lyrics,
			Keywords:  append([]string(nil), recipe.MusicRecipe.Lyrics.Keywords...),
			Mood:      recipe.MusicRecipe.Lyrics.Mood,
			Narrative: recipe.MusicRecipe.Lyrics.Narrative,
		}
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
