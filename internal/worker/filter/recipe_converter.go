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

	for i, cut := range recipe.Cuts {
		if strings.TrimSpace(cut.Dialogue) != "" {
			continue
		}
		for _, sec := range recipe.MusicRecipe.Sections {
			sStart := float64(sec.StartSeconds)
			sEnd := float64(sec.EndSeconds)
			if sEnd <= sStart && sec.Duration > 0 {
				sEnd = sStart + float64(sec.Duration)
			}
			if cut.StartSec < sStart || cut.StartSec >= sEnd {
				continue
			}
			lines, ok := sectionLines[sec.Name]
			if !ok || len(lines) == 0 {
				break
			}
			// このセクションに属するカットを集め、その中での位置を割り出す
			var secCuts []int // recipe.Cuts のスライスインデックス
			for j, c := range recipe.Cuts {
				if c.StartSec >= sStart && c.StartSec < sEnd {
					secCuts = append(secCuts, j)
				}
			}
			pos := 0
			for p, idx := range secCuts {
				if idx == i {
					pos = p
					break
				}
			}
			recipe.Cuts[i].Dialogue = assignLinesForCut(lines, pos, len(secCuts))
			break
		}
	}
}

// parseLyricsSections は "[Section Name]\nline...\n\n[Next]..." 形式の歌詞テキストを
// セクション名→歌詞行スライスのマップに変換します。
func parseLyricsSections(lyricsText string) map[string][]string {
	result := make(map[string][]string)
	current := ""
	for _, line := range strings.Split(lyricsText, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = line[1 : len(line)-1]
			continue
		}
		if line != "" && current != "" {
			result[current] = append(result[current], line)
		}
	}
	return result
}

// assignLinesForCut はセクション内の歌詞行をカット数で均等分割し、
// pos 番目のカットに割り当てる行を改行結合した文字列で返します。
func assignLinesForCut(lines []string, pos, totalCuts int) string {
	if totalCuts <= 1 {
		return strings.Join(lines, "\n")
	}
	base := len(lines) / totalCuts
	if base == 0 {
		base = 1
	}
	start := pos * base
	end := start + base
	if pos == totalCuts-1 {
		end = len(lines)
	}
	if start >= len(lines) {
		return ""
	}
	if end > len(lines) {
		end = len(lines)
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
