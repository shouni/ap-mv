package domain

import (
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
