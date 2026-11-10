package step

import (
	"fmt"
	"path"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

// toVideoRecipe converts a domain music recipe to an orchestrator video recipe.
//
// The actual construction (filling titles from each other, expanding sections into cuts, deep
// copying) belongs to video.NewRecipeFromMusic. A hand-written shallow copy used to live here,
// leaving room to drop fields whenever the music side gained one.
func toVideoRecipe(recipe *domain.MusicRecipe) (*video.Recipe, error) {
	if recipe == nil {
		return nil, nil
	}
	if err := domain.NormalizeMusicRecipe(recipe); err != nil {
		return nil, err
	}
	return video.NewRecipeFromMusic(*recipe), nil
}

// applyTaskAudioURLToVideoRecipe applies a task audio URL to an orchestrator recipe.
// Only the command check (video_recipe_create does not attach audio) is the app's policy;
// the filling itself belongs to video.Cuts.FillAudioReference.
func applyTaskAudioURLToVideoRecipe(task *domain.Task, recipe *video.Recipe) {
	if task == nil || recipe == nil {
		return
	}
	if task.Command == domain.CommandVideoRecipeCreate {
		return
	}
	recipe.Normalize()
	video.Cuts(recipe.Cuts).FillAudioReference(task.AudioURL)
}

// applyTaskCharacterIDToVideoRecipe applies a task character ID to an orchestrator recipe.
func applyTaskCharacterIDToVideoRecipe(task *domain.Task, recipe *video.Recipe) {
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

// originalJobOutputPath derives the job root path (e.g. gs://bucket/jobs/{jobID}/) from RecipeURL
// (e.g. gs://bucket/jobs/{jobID}/video_music_meta.json).
// path.Dir collapses the // of gs://, so the scheme is split off with remoteio before operating.
func originalJobOutputPath(recipeURL string) string {
	recipeURL = strings.TrimSpace(recipeURL)
	if recipeURL == "" {
		return ""
	}
	bucket, objPath, err := remoteio.ParseBucketURI(remoteio.SchemeGCS, recipeURL)
	if err != nil {
		return ""
	}
	dir := path.Dir(objPath)
	if dir == "." || dir == "" || dir == "/" {
		return ""
	}
	return remoteio.BuildURI(remoteio.SchemeGCS, bucket, dir) + "/"
}

// applyLyricsToVideoRecipeCuts is the step-local wrapper of domain.ApplyLyricsToVideoRecipeCuts.
func applyLyricsToVideoRecipeCuts(recipe *video.Recipe) {
	domain.ApplyLyricsToVideoRecipeCuts(recipe)
}

// buildInputsTxt builds an ffmpeg concat demuxer inputs.txt from recipe cuts.
// The implementation is domain.BuildFFmpegInputsTxt, the same rule as repository's keyframe ZIP.
func buildInputsTxt(cuts []video.Cut) string {
	return domain.BuildFFmpegInputsTxt(cuts)
}

// orchestratorCutsToHistoryCuts converts orchestrator cuts to domain history cuts for ASS generation.
// The implementation is domain.VideoCutsToHistoryCuts (domain.VideoCut = video.Cut).
func orchestratorCutsToHistoryCuts(cuts []video.Cut) []domain.VideoHistoryCut {
	return domain.VideoCutsToHistoryCuts(cuts)
}

// findCutByIndex returns the slice index matching the given cutIndex in the recipe.
// The search belongs to video.Cuts.IndexOf; this only adds error context.
func findCutByIndex(cuts []video.Cut, cutIndex int) (int, error) {
	if i := video.Cuts(cuts).IndexOf(cutIndex); i >= 0 {
		return i, nil
	}
	return -1, fmt.Errorf("cut index %d not found in recipe", cutIndex)
}

// toDomainRecipe converts an orchestrator video recipe to a domain music recipe.
//
// domain.MusicRecipe is the same type as VideoRecipe.MusicRecipe (an alias of music.Recipe), so
// the conversion is just a deep copy (Clone). It used to copy fields one by one and silently
// dropped fields missing from the list, such as Key / VocalProfile.
func toDomainRecipe(recipe *video.Recipe) (*domain.MusicRecipe, error) {
	if recipe == nil {
		return nil, nil
	}
	recipe.Normalize()

	domainRecipe := recipe.MusicRecipe.Clone()
	if domainRecipe.Title == "" {
		domainRecipe.Title = recipe.ProjectTitle
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
