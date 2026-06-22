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

// applyLyricsToVideoRecipeCuts は music_recipe の歌詞テキストをセクション単位に分解し、
// 各カットの StartSec が属するセクションの歌詞行を cut.Dialogue に書き込みます。
// すでに Dialogue が設定済みのカットはスキップします。
func applyLyricsToVideoRecipeCuts(recipe *orchestrator.VideoRecipe) {
	if recipe == nil || recipe.MusicRecipe.Lyrics == nil {
		return
	}
	lyricsText := strings.TrimSpace(recipe.MusicRecipe.Lyrics.Lyrics)
	if lyricsText == "" {
		return
	}
	sectionLines := parseLyricsSections(lyricsText)

	// 事前にセクション名→カットスライスインデックスのマップを構築して O(N) にする
	secCutsMap := make(map[string][]int)
	for i, cut := range recipe.Cuts {
		for _, sec := range recipe.MusicRecipe.Sections {
			sStart := float64(sec.StartSeconds)
			sEnd := float64(sec.EndSeconds)
			if sEnd <= sStart && sec.Duration > 0 {
				sEnd = sStart + float64(sec.Duration)
			}
			if cut.StartSec >= sStart && cut.StartSec < sEnd {
				secCutsMap[sec.Name] = append(secCutsMap[sec.Name], i)
				break
			}
		}
	}

	for secName, cutIndices := range secCutsMap {
		lines, ok := sectionLines[secName]
		if !ok || len(lines) == 0 {
			continue
		}
		for pos, idx := range cutIndices {
			if strings.TrimSpace(recipe.Cuts[idx].Dialogue) == "" {
				recipe.Cuts[idx].Dialogue = assignLinesForCut(lines, pos, len(cutIndices))
			}
		}
	}
}

// parseLyricsSections は "[Section Name]\nline...\n\n[Next]..." 形式の歌詞テキストを
// セクション名→歌詞行スライスのマップに変換します。
// セクションヘッダーより前の行は "Default" セクションとして扱います。
func parseLyricsSections(lyricsText string) map[string][]string {
	result := make(map[string][]string)
	current := "Default"
	for _, line := range strings.Split(lyricsText, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = line[1 : len(line)-1]
			continue
		}
		if line != "" {
			result[current] = append(result[current], line)
		}
	}
	return result
}

// assignLinesForCut はセクション内の歌詞行をカット数で均等分割し、
// pos 番目のカットに割り当てる行を改行結合した文字列で返します。
// (pos*N)/totalCuts 方式で余りを均等に分散させます。
func assignLinesForCut(lines []string, pos, totalCuts int) string {
	if totalCuts <= 1 {
		return strings.Join(lines, "\n")
	}
	n := len(lines)
	start := (pos * n) / totalCuts
	end := ((pos + 1) * n) / totalCuts
	if start >= n {
		return ""
	}
	if end > n {
		end = n
	}
	return strings.Join(lines[start:end], "\n")
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
