package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shouni/go-gemini-client/lyria"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
)

// LyricsDraft は go-gemini-client が定義する作詞フェーズの出力です。
type LyricsDraft = lyria.LyricsDraft

// MusicRecipe は go-gemini-client/lyria の楽曲設計図です。
type MusicRecipe = lyria.MusicRecipe

// MusicSection は go-gemini-client/lyria の曲内セクションです。
type MusicSection = lyria.MusicSection

// VideoRecipe は go-veo-orchestrator が定義する動画台本です。
type VideoRecipe = orchestrator.VideoRecipe

// VideoCut は go-veo-orchestrator が定義する動画カットです。
type VideoCut = orchestrator.Cut

const CutStatusGenerated = string(orchestrator.CutStatusGenerated)

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
	Seed        *int64         `json:"seed,omitempty"`
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
	if recipe.MusicRecipe.Seed == nil && legacy.Seed != nil {
		seed := *legacy.Seed
		recipe.MusicRecipe.Seed = &seed
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
func ValidateVideoRecipe(r *VideoRecipe) error {
	if r == nil {
		return fmt.Errorf("video recipe is nil")
	}
	if strings.TrimSpace(firstNonEmpty(r.ProjectTitle, r.MusicRecipe.Title)) == "" {
		return fmt.Errorf("video recipe title is required")
	}
	if len(r.Cuts) == 0 {
		return fmt.Errorf("video recipe requires cuts")
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// ApplyLyricsToVideoRecipeCuts は music_recipe の歌詞テキストをセクション単位に分解し、
// 各カットの StartSec が属するセクションの歌詞行を cut.Dialogue に書き込みます。
// すでに Dialogue が設定済みのカットはスキップします。
// DownloadKeyframes など、保存済みレシピを読んで処理する経路でも呼び出せます。
func ApplyLyricsToVideoRecipeCuts(recipe *VideoRecipe) {
	if recipe == nil || recipe.MusicRecipe.Lyrics == nil {
		return
	}
	lyricsText := strings.TrimSpace(recipe.MusicRecipe.Lyrics.Lyrics)
	if lyricsText == "" {
		return
	}
	sectionLines := parseLyricsSections(lyricsText)

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
		if section.EndSeconds <= section.StartSeconds {
			section.EndSeconds = section.StartSeconds + section.Duration
		}
		cursor = section.EndSeconds
	}
	return ValidateMusicRecipe(r)
}
