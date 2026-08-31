package filter

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
// 実体の組み立て（タイトルの相互補完・セクション→カット展開・深いコピー）は
// video.NewRecipeFromMusic が持ちます。以前はここで手書きの浅いコピーを
// していましたが、音楽側にフィールドが増えるたびに取りこぼしの余地がありました。
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
// コマンド判定（video_recipe_create は音源を紐づけない）だけがアプリの方針で、
// 充填そのものは video.Cuts.FillAudioReference が持ちます。
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

// originalJobOutputPath は RecipeURL（例: gs://bucket/jobs/{jobID}/video_music_meta.json）から
// ジョブルートパス（例: gs://bucket/jobs/{jobID}/）を導出します。
// path.Dir は gs:// の // を潰すため remoteio でスキームを分解してから操作します。
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

// applyLyricsToVideoRecipeCuts は domain.ApplyLyricsToVideoRecipeCuts の filter 内ラッパーです。
func applyLyricsToVideoRecipeCuts(recipe *video.Recipe) {
	domain.ApplyLyricsToVideoRecipeCuts(recipe)
}

// buildInputsTxt builds an ffmpeg concat demuxer inputs.txt from recipe cuts.
// 実体は domain.BuildFFmpegInputsTxt で、repository のキーフレーム ZIP と同じ規則です。
func buildInputsTxt(cuts []video.Cut) string {
	return domain.BuildFFmpegInputsTxt(cuts)
}

// orchestratorCutsToHistoryCuts converts orchestrator cuts to domain history cuts for ASS generation.
// 実体は domain.VideoCutsToHistoryCuts です（domain.VideoCut = video.Cut）。
func orchestratorCutsToHistoryCuts(cuts []video.Cut) []domain.VideoHistoryCut {
	return domain.VideoCutsToHistoryCuts(cuts)
}

// findCutByIndex はレシピ内の指定 cutIndex に対応するスライスインデックスを返します。
// 探索は video.Cuts.IndexOf が持ち、ここではエラー文脈だけを付けます。
func findCutByIndex(cuts []video.Cut, cutIndex int) (int, error) {
	if i := video.Cuts(cuts).IndexOf(cutIndex); i >= 0 {
		return i, nil
	}
	return -1, fmt.Errorf("cut index %d not found in recipe", cutIndex)
}

// toDomainRecipe converts an orchestrator video recipe to a domain music recipe.
//
// domain.MusicRecipe は VideoRecipe.MusicRecipe と同じ型（music.Recipe の別名）なので、
// 変換の実体は深いコピー（Clone）だけです。以前はフィールドを1つずつ写し取っており、
// Key / VocalProfile など列挙から漏れたフィールドを silently drop していました。
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
