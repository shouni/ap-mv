package domain

import (
	"fmt"
	"strings"
)

// LyricsDraft は作詞フェーズの出力です。
type LyricsDraft struct {
	Title     string   `json:"title"`
	Theme     string   `json:"theme"`
	Hook      string   `json:"hook"`
	Lyrics    string   `json:"lyrics"`
	Keywords  []string `json:"keywords,omitempty"`
	Mood      string   `json:"mood,omitempty"`
	Narrative string   `json:"narrative,omitempty"`
}

// MusicRecipe は楽曲設計図です。
type MusicRecipe struct {
	Title       string         `json:"title"`
	Theme       string         `json:"theme"`
	Mood        string         `json:"mood"`
	Tempo       int            `json:"tempo"`
	Instruments []string       `json:"instruments"`
	Sections    []MusicSection `json:"sections"`
	Cuts        []VideoCut     `json:"cuts,omitempty"`
	Lyrics      *LyricsDraft   `json:"lyrics,omitempty"`
	AIModels
}

// MusicSection は曲内セクションです。
type MusicSection struct {
	Name     string `json:"name"`
	Duration int    `json:"duration_seconds"`
	Prompt   string `json:"prompt"`
	AudioCue string `json:"audio_cue,omitempty"`
}

// VideoCut はVeoへ渡す1カット分のタイムライン単位です。
type VideoCut struct {
	Index        int    `json:"index"`
	SectionName  string `json:"section_name,omitempty"`
	StartSec     int    `json:"start_sec"`
	EndSec       int    `json:"end_sec"`
	DurationSec  int    `json:"duration_sec"`
	Prompt       string `json:"prompt"`
	AudioCue     string `json:"audio_cue,omitempty"`
	Status       string `json:"status,omitempty"`
	VideoID      string `json:"video_id,omitempty"`
	VideoURL     string `json:"video_url,omitempty"`
	KeyframeURI  string `json:"keyframe_uri,omitempty"`
	AudioURI     string `json:"audio_uri,omitempty"`
	ImageRefName string `json:"image_ref_name,omitempty"`
}

const CutStatusGenerated = "generated"

// Validate checks the receiver for invalid state.
func (r *MusicRecipe) Validate() error {
	if r == nil {
		return fmt.Errorf("music recipe is nil")
	}
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("recipe title is required")
	}
	if len(r.Sections) == 0 && len(r.Cuts) == 0 {
		return fmt.Errorf("recipe requires sections or cuts")
	}
	for i, section := range r.Sections {
		if section.Duration <= 0 {
			return fmt.Errorf("section %d duration_seconds must be positive", i)
		}
	}
	for i, cut := range r.Cuts {
		if cut.DurationSec <= 0 {
			return fmt.Errorf("cut %d duration_sec must be positive", i)
		}
	}
	return nil
}

// Normalize はsectionsベースのレシピからcutsを補完し、開始/終了秒を整えます。
func (r *MusicRecipe) Normalize() error {
	if err := r.Validate(); err != nil {
		return err
	}
	if len(r.Cuts) == 0 {
		r.Cuts = make([]VideoCut, 0, len(r.Sections))
		cursor := 0
		for i, section := range r.Sections {
			cut := VideoCut{
				Index:       i,
				SectionName: strings.TrimSpace(section.Name),
				StartSec:    cursor,
				EndSec:      cursor + section.Duration,
				DurationSec: section.Duration,
				Prompt:      strings.TrimSpace(section.Prompt),
				AudioCue:    strings.TrimSpace(section.AudioCue),
			}
			if cut.Prompt == "" {
				cut.Prompt = fmt.Sprintf("Music video cut for %s: %s", r.Title, cut.SectionName)
			}
			r.Cuts = append(r.Cuts, cut)
			cursor = cut.EndSec
		}
		return nil
	}
	cursor := 0
	for i := range r.Cuts {
		if r.Cuts[i].Index == 0 && i > 0 {
			r.Cuts[i].Index = i
		}
		if r.Cuts[i].StartSec == 0 && i > 0 {
			r.Cuts[i].StartSec = cursor
		}
		if r.Cuts[i].EndSec <= r.Cuts[i].StartSec {
			r.Cuts[i].EndSec = r.Cuts[i].StartSec + r.Cuts[i].DurationSec
		}
		if r.Cuts[i].DurationSec <= 0 {
			r.Cuts[i].DurationSec = r.Cuts[i].EndSec - r.Cuts[i].StartSec
		}
		cursor = r.Cuts[i].EndSec
	}
	return r.Validate()
}
