package filter

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

type ScriptingFilter struct{}

func (ScriptingFilter) Name() string { return "scripting" }

func (ScriptingFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil || fc.Task == nil {
		return fmt.Errorf("scripting context requires task")
	}
	if fc.VideoRecipe != nil {
		return nil
	}
	if fc.Recipe != nil {
		if err := applyTaskAudioURL(fc.Task, fc.Recipe); err != nil {
			return err
		}
		recipe, err := toVideoRecipe(fc.Recipe)
		if err != nil {
			return err
		}
		applyTaskAudioURLToVideoRecipe(fc.Task, recipe)
		applyTaskCharacterIDToVideoRecipe(fc.Task, recipe)
		fc.VideoRecipe = recipe
		return nil
	}
	if fc.Workflows == nil || fc.Workflows.Script == nil {
		return fmt.Errorf("script workflow is not configured")
	}
	source := scriptSource(fc.Task.SourceURL, fc.Task.Text, fc.Task.ImageURL)
	if source == "" {
		return fmt.Errorf("scripting requires source text, url, or image")
	}
	recipe, err := fc.Workflows.Script.Run(ctx, source, string(fc.Task.Command))
	if err != nil {
		return err
	}
	applyTaskAudioURLToVideoRecipe(fc.Task, recipe)
	applyTaskCharacterIDToVideoRecipe(fc.Task, recipe)
	fc.VideoRecipe = recipe
	domainRecipe, err := toDomainRecipe(recipe)
	if err != nil {
		return err
	}
	fc.Recipe = domainRecipe
	return nil
}

func scriptSource(sourceURL, text, imageURL string) string {
	if sourceURL = strings.TrimSpace(sourceURL); sourceURL != "" {
		return sourceURL
	}
	if text = strings.TrimSpace(text); text != "" {
		return "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte(text))
	}
	return strings.TrimSpace(imageURL)
}
