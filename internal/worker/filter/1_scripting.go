package filter

import (
	"context"
	"fmt"
	"strings"

	"ap-mv/internal/domain"
)

type ScriptingFilter struct{}

func (ScriptingFilter) Name() string { return "scripting" }

func (ScriptingFilter) Execute(_ context.Context, fc *Context) error {
	if fc == nil || fc.Task == nil {
		return fmt.Errorf("scripting context requires task")
	}
	if fc.Recipe != nil {
		return nil
	}
	title := "AP MV Generated Recipe"
	text := strings.TrimSpace(fc.Task.Text)
	if text == "" {
		text = strings.TrimSpace(fc.Task.SourceURL)
	}
	if text == "" {
		text = strings.TrimSpace(fc.Task.ImageURL)
	}
	if text == "" {
		return fmt.Errorf("scripting requires source text, url, or image")
	}
	fc.Recipe = &domain.MusicRecipe{
		Title: title,
		Theme: text,
		Mood:  "cinematic",
		Tempo: 120,
		Sections: []domain.MusicSection{
			{Name: "intro", Duration: 8, Prompt: text, AudioCue: "opening phrase"},
		},
	}
	return fc.Recipe.Normalize()
}
