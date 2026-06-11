package filter

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ap-mv/internal/domain"
	"ap-mv/internal/ports"
)

type VideoGenerationFilter struct {
	Runner ports.VideoRunner
}

// Name returns the receiver name.
func (VideoGenerationFilter) Name() string { return "video_gen" }

// Execute runs the receiver processing step.
func (f VideoGenerationFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil || fc.Task == nil {
		return fmt.Errorf("video generation requires task and recipe")
	}
	ctx = videoOutputContext(ctx, fc)
	if fc.Workflows != nil && fc.Workflows.Video != nil {
		if fc.VideoRecipe == nil {
			if err := applyTaskAudioURL(fc.Task, fc.Recipe); err != nil {
				return err
			}
			recipe, err := toVideoRecipe(fc.Recipe)
			if err != nil {
				return err
			}
			fc.VideoRecipe = recipe
		}
		applyTaskAudioURLToVideoRecipe(fc.Task, fc.VideoRecipe)
		if fc.VideoRecipe == nil {
			return fmt.Errorf("video generation requires recipe")
		}
		if _, err := fc.Workflows.Video.Run(ctx, fc.VideoRecipe); err != nil {
			return err
		}
		recipe, err := toDomainRecipe(fc.VideoRecipe)
		fc.Recipe = recipe
		return err
	}
	if fc.Recipe == nil {
		return fmt.Errorf("video generation requires recipe")
	}

	if err := fc.Recipe.Normalize(); err != nil {
		return err
	}
	runner := f.Runner
	if runner == nil {
		runner = fc.VideoRunner
	}
	if runner == nil {
		return fmt.Errorf("video runner is not configured")
	}

	lastVideoID := ""
	for i := range fc.Recipe.Cuts {
		cut := &fc.Recipe.Cuts[i]
		if cut.Status == domain.CutStatusGenerated {
			if cut.VideoID != "" {
				lastVideoID = cut.VideoID
			}
			continue
		}
		seed := int64(0)
		if fc.Recipe.Seed != nil {
			seed = *fc.Recipe.Seed
		}
		res, err := runner.Run(ctx, ports.VideoGenerationRequest{
			CutIndex:        cut.Index,
			Prompt:          videoPrompt(*cut),
			DurationSec:     float64(cut.DurationSec),
			Seed:            seed,
			PreviousVideoID: lastVideoID,
			ImageReference:  cut.KeyframeURI,
			AudioReference:  cut.AudioURI,
		})
		if err != nil {
			return fmt.Errorf("generate cut %d: %w", cut.Index, err)
		}
		cut.Status = domain.CutStatusGenerated
		cut.VideoID = res.VideoID
		cut.VideoURL = res.CloudURL
		lastVideoID = res.VideoID
		if hasPendingCuts(fc.Recipe) && fc.TaskQueue != nil {
			nextTask := *fc.Task
			nextTask.Command = domain.CommandGenerateFromRecipe
			nextTask.Recipe = fc.Recipe
			nextTask.CreatedAt = time.Now().UTC()
			if err := fc.TaskQueue.Enqueue(ctx, &nextTask); err != nil {
				return fmt.Errorf("enqueue continuation after cut %d: %w", cut.Index, err)
			}
			return ErrPipelineDeferred
		}
	}
	return nil
}

// videoOutputContext adds the output base URI to the context when available.
func videoOutputContext(ctx context.Context, fc *Context) context.Context {
	if fc == nil {
		return ctx
	}
	return ports.WithVideoOutputBaseURI(ctx, fc.OutputPath)
}

// hasPendingCuts reports whether a recipe has cuts awaiting video generation.
func hasPendingCuts(recipe *domain.MusicRecipe) bool {
	if recipe == nil {
		return false
	}
	for i := range recipe.Cuts {
		if recipe.Cuts[i].Status != domain.CutStatusGenerated {
			return true
		}
	}
	return false
}

// videoPrompt builds the prompt used for video generation.
func videoPrompt(cut domain.VideoCut) string {
	parts := []string{
		strings.TrimSpace(cut.Prompt),
	}
	if cue := strings.TrimSpace(cut.AudioCue); cue != "" {
		parts = append(parts, "Synchronize motion and camera timing with audio cue: "+cue)
	}
	if section := strings.TrimSpace(cut.SectionName); section != "" {
		parts = append(parts, "Section: "+section)
	}

	nonEmpty := parts[:0]
	for _, part := range parts {
		if part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, "\n")
}
