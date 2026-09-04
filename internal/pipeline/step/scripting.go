package step

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

// ScriptingStep は、歌詞・レシピから台本（VideoRecipe）を生成するパイプラインステップです。
type ScriptingStep struct{}

// Name returns the receiver name.
func (ScriptingStep) Name() string { return "scripting" }

// Execute runs the receiver processing step.
func (ScriptingStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil || sc.Task == nil {
		return fmt.Errorf("scripting context requires task")
	}
	if sc.VideoRecipe != nil {
		return nil
	}
	if sc.Recipe != nil {
		recipe, err := toVideoRecipe(sc.Recipe)
		if err != nil {
			return err
		}
		applyTaskAudioURLToVideoRecipe(sc.Task, recipe)
		applyTaskCharacterIDToVideoRecipe(sc.Task, recipe)
		sc.VideoRecipe = recipe
		return nil
	}
	if sc.Workflows == nil || sc.Workflows.Script == nil {
		return fmt.Errorf("script workflow is not configured")
	}
	source := scriptSource(sc.Task.SourceURL, sc.Task.Text, sc.Task.ImageURL)
	if source == "" {
		return fmt.Errorf("scripting requires source text, url, or image")
	}
	recipe, err := sc.Workflows.Script.Run(ctx, source, string(sc.Task.Command))
	if err != nil {
		return err
	}
	applyTaskAudioURLToVideoRecipe(sc.Task, recipe)
	applyTaskCharacterIDToVideoRecipe(sc.Task, recipe)
	sc.VideoRecipe = recipe
	domainRecipe, err := toDomainRecipe(recipe)
	if err != nil {
		return err
	}
	sc.Recipe = domainRecipe
	return nil
}

// scriptSource returns the best source value for script generation.
func scriptSource(sourceURL, text, imageURL string) string {
	if sourceURL = strings.TrimSpace(sourceURL); sourceURL != "" {
		return sourceURL
	}
	if text = strings.TrimSpace(text); text != "" {
		return "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte(text))
	}
	return strings.TrimSpace(imageURL)
}
