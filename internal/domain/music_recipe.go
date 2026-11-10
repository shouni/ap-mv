package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shouni/genai-kit/music"
	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"
)

// LyricsDraft is the output of the lyric-writing phase defined by genai-kit/music.
type LyricsDraft = music.LyricsDraft

// MusicRecipe is genai-kit/music's song blueprint.
// It is a types-only leaf package, so the whole lyria generation workflow does not have to be imported.
type MusicRecipe = music.Recipe

// MusicSection is a section within a song from genai-kit/music.
type MusicSection = music.Section

// VideoRecipe is the video script defined by go-veo-orchestrator.
type VideoRecipe = video.Recipe

// VideoCut is the video cut defined by go-veo-orchestrator.
type VideoCut = video.Cut

// CutStatusGenerated is the status value of a cut whose keyframe generation has finished.
const CutStatusGenerated = string(video.CutStatusGenerated)

// UnmarshalRecipeOrVideoRecipe parses either a MusicRecipe JSON or a VideoRecipe JSON.
func UnmarshalRecipeOrVideoRecipe(raw []byte) (*MusicRecipe, *VideoRecipe, error) {
	if looksLikeVideoRecipeJSON(raw) {
		recipe, err := DecodeVideoRecipeJSON(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("decode video recipe json: %w", err)
		}
		if err := ValidateVideoRecipe(recipe); err != nil {
			return nil, nil, err
		}
		return nil, recipe, nil
	}

	var recipe MusicRecipe
	if err := json.Unmarshal(raw, &recipe); err != nil {
		return nil, nil, fmt.Errorf("decode recipe json: %w", err)
	}
	if err := NormalizeMusicRecipe(&recipe); err != nil {
		return nil, nil, err
	}
	return &recipe, nil, nil
}

// DecodeVideoRecipeJSON decodes current and legacy VideoRecipe JSON shapes.
func DecodeVideoRecipeJSON(raw []byte) (*VideoRecipe, error) {
	var combined struct {
		VideoRecipe
		legacyVideoRecipeFields
	}
	if err := json.Unmarshal(raw, &combined); err != nil {
		return nil, err
	}
	recipe := combined.VideoRecipe
	applyLegacyVideoRecipeFields(&recipe, combined.legacyVideoRecipeFields)
	recipe.Normalize()
	return &recipe, nil
}

type legacyVideoRecipeFields struct {
	Title       string         `json:"title,omitempty"`
	Theme       string         `json:"theme,omitempty"`
	Mood        string         `json:"mood,omitempty"`
	Tempo       int            `json:"tempo,omitempty"`
	Instruments []string       `json:"instruments,omitempty"`
	Sections    []MusicSection `json:"sections,omitempty"`
	Lyrics      *LyricsDraft   `json:"lyrics,omitempty"`
	AudioModel  string         `json:"audio_model,omitempty"`
	ComposeMode string         `json:"compose_mode,omitempty"`
}

func applyLegacyVideoRecipeFields(recipe *VideoRecipe, legacy legacyVideoRecipeFields) {
	if recipe.MusicRecipe.Title == "" {
		recipe.MusicRecipe.Title = legacy.Title
	}
	if recipe.MusicRecipe.Theme == "" {
		recipe.MusicRecipe.Theme = legacy.Theme
	}
	if recipe.MusicRecipe.Mood == "" {
		recipe.MusicRecipe.Mood = legacy.Mood
	}
	if recipe.MusicRecipe.Tempo == 0 {
		recipe.MusicRecipe.Tempo = legacy.Tempo
	}
	if len(recipe.MusicRecipe.Instruments) == 0 {
		recipe.MusicRecipe.Instruments = append([]string(nil), legacy.Instruments...)
	}
	if len(recipe.MusicRecipe.Sections) == 0 {
		recipe.MusicRecipe.Sections = append([]MusicSection(nil), legacy.Sections...)
	}
	if recipe.MusicRecipe.Lyrics == nil {
		recipe.MusicRecipe.Lyrics = legacy.Lyrics
	}
	if recipe.MusicRecipe.AudioModel == "" {
		recipe.MusicRecipe.AudioModel = legacy.AudioModel
	}
	if recipe.MusicRecipe.ComposeMode == "" {
		recipe.MusicRecipe.ComposeMode = legacy.ComposeMode
	}
}

func looksLikeVideoRecipeJSON(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return false
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok {
			return false
		}
		if key == "cuts" || key == "project_title" || key == "music_recipe" {
			return true
		}
		if err := skipJSONValue(decoder); err != nil {
			return false
		}
	}
	return false
}

func skipJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for decoder.More() {
			if _, err := decoder.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

// ValidateMusicRecipe checks the receiver for invalid state.
func ValidateMusicRecipe(r *MusicRecipe) error {
	if r == nil {
		return fmt.Errorf("music recipe is nil")
	}
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("recipe title is required")
	}
	if len(r.Sections) == 0 {
		return fmt.Errorf("recipe requires sections")
	}
	for i, section := range r.Sections {
		if section.Duration <= 0 && section.EndSeconds <= section.StartSeconds {
			return fmt.Errorf("section %d duration_seconds must be positive", i)
		}
	}
	return nil
}

// ValidateVideoRecipe checks the receiver for invalid state.
//
// The implementation is VideoRecipe's (the orchestrator type's) own Validate. A type's invariants
// live with the type, so the same rule applies both right after AI generation (VideoScriptRunner) and when hand input is submitted.
func ValidateVideoRecipe(r *VideoRecipe) error {
	return r.Validate()
}

// ApplyLyricsToVideoRecipeCuts splits the music_recipe's lyric text by section and writes the
// lyric lines of each cut's section (cut.SectionIndex, 1-based; VideoRecipe.Normalize fills it
// from StartSec) into cut.Dialogue. Cuts that already have Dialogue are skipped. It can also be
// called from paths that read and process a stored recipe, such as DownloadKeyframes.
func ApplyLyricsToVideoRecipeCuts(recipe *VideoRecipe) {
	if recipe == nil || recipe.MusicRecipe.Lyrics == nil {
		return
	}
	lyricsText := strings.TrimSpace(recipe.MusicRecipe.Lyrics.Lyrics)
	if lyricsText == "" {
		return
	}
	// cut.SectionIndex decides which section a cut belongs to, so make sure it is filled in here
	// (in case the caller has not normalised yet). Normalize is idempotent, so calling it again
	// is harmless.
	recipe.Normalize()
	sectionLines := parseLyricsSections(lyricsText)

	secCutsMap := make(map[string][]int)
	for i, cut := range recipe.Cuts {
		if cut.SectionIndex < 1 || cut.SectionIndex > len(recipe.MusicRecipe.Sections) {
			continue
		}
		secName := recipe.MusicRecipe.Sections[cut.SectionIndex-1].Name
		secCutsMap[secName] = append(secCutsMap[secName], i)
	}

	for secName, cutIndices := range secCutsMap {
		lines, ok := sectionLines[secName]
		if !ok || len(lines) == 0 {
			continue
		}
		for pos, idx := range cutIndices {
			if strings.TrimSpace(recipe.Cuts[idx].Dialogue) == "" {
				recipe.Cuts[idx].Dialogue = DistributeLines(lines, pos, len(cutIndices))
			}
		}
	}
}

func parseLyricsSections(lyricsText string) map[string][]string {
	result := make(map[string][]string)
	current := "Default"
	for line := range strings.SplitSeq(lyricsText, "\n") {
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

// NormalizeModelList returns a deduplicated model list with `preferred` first (if non-empty),
// followed by the remaining unique values in order. An empty result stays empty — there is no
// built-in fallback model; Config.ValidateEssentialConfig reports a missing list at startup.
func NormalizeModelList(values []string, preferred string) []string {
	seen := make(map[string]bool, len(values)+1)
	result := make([]string, 0, len(values)+1)
	if preferred = strings.TrimSpace(preferred); preferred != "" {
		result = append(result, preferred)
		seen[preferred] = true
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result
}

// NormalizeDefaultModel selects a valid default model: value if non-empty, otherwise the first
// entry of models. Returns an empty string when neither is set — see NormalizeModelList.
func NormalizeDefaultModel(value string, models []string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	if len(models) > 0 {
		return models[0]
	}
	return ""
}

// SectionEndSeconds derives a section's end time in seconds, falling back to
// startSeconds+duration when endSeconds has not been set (endSeconds <= startSeconds).
// If duration is also unset, endSeconds is returned unchanged.
func SectionEndSeconds(startSeconds, endSeconds, duration int) int {
	if endSeconds <= startSeconds && duration > 0 {
		return startSeconds + duration
	}
	return endSeconds
}

// DistributeLines splits lines proportionally across `total` buckets and returns the lines
// assigned to bucket `pos` (0-indexed), joined by newlines.
// The implementation moved to go-veo-orchestrator along with the duration planner (the
// distribution rule has to match sub-cut splitting, so there is one definition).
func DistributeLines(lines []string, pos, total int) string {
	return veo.DistributeLines(lines, pos, total)
}

// NormalizeMusicRecipe fills section time ranges for a music recipe.
func NormalizeMusicRecipe(r *MusicRecipe) error {
	if err := ValidateMusicRecipe(r); err != nil {
		return err
	}
	cursor := 0
	for i := range r.Sections {
		section := &r.Sections[i]
		if section.Duration <= 0 {
			section.Duration = section.EndSeconds - section.StartSeconds
		}
		if section.StartSeconds == 0 && i > 0 {
			section.StartSeconds = cursor
		}
		section.EndSeconds = SectionEndSeconds(section.StartSeconds, section.EndSeconds, section.Duration)
		cursor = section.EndSeconds
	}
	return ValidateMusicRecipe(r)
}
