package filter

import (
	"context"
	"fmt"
	"strings"
	"time"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

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
		if fc.VideoRecipe == nil {
			return fmt.Errorf("video generation requires recipe")
		}
	}

	if fc.VideoRecipe == nil {
		recipe, err := toVideoRecipe(fc.Recipe)
		if err != nil {
			return err
		}
		fc.VideoRecipe = recipe
	}
	applyTaskAudioURLToVideoRecipe(fc.Task, fc.VideoRecipe)
	applyTaskCharacterIDToVideoRecipe(fc.Task, fc.VideoRecipe)
	fc.VideoRecipe.Normalize()
	runner := f.Runner
	if runner == nil {
		runner = fc.VideoRunner
	}
	if runner == nil {
		return fmt.Errorf("video runner is not configured")
	}

	lastVideoID := ""
	for i := range fc.VideoRecipe.Cuts {
		cut := &fc.VideoRecipe.Cuts[i]
		if cut.IsGenerated() {
			if cut.VideoID != "" {
				lastVideoID = cut.VideoID
			}
			continue
		}
		res, err := runner.Run(ctx, ports.VideoGenerationRequest{
			CutIndex:        cut.CutIndex,
			Prompt:          videoPrompt(*cut),
			DurationSec:     cut.DurationSec,
			Seed:            seedValue(fc.VideoRecipe.MusicRecipe.Seed),
			PreviousVideoID: lastVideoID,
			ImageReference:  cut.KeyframeReference,
			AudioReference:  cut.AudioReference,
		})
		if err != nil {
			return fmt.Errorf("generate cut %d: %w", cut.CutIndex, err)
		}
		cut.Status = orchestrator.CutStatusGenerated
		cut.VideoID = res.VideoID
		cut.VideoURL = res.CloudURL
		lastVideoID = res.VideoID
		// 継続タスクのエンキューに失敗した場合、Cloud Tasks は元のタスクを再試行する。
		// 再試行時には直前の続きタスクのペイロード（このカットはまだ pending）から再開するため、
		// カットが再生成される可能性があるが、状態の整合性は保たれる。
		if hasPendingCuts(fc.VideoRecipe) && fc.TaskQueue != nil {
			domainRecipe, err := toDomainRecipe(fc.VideoRecipe)
			if err != nil {
				return err
			}
			fc.Recipe = domainRecipe
			nextTask := *fc.Task
			nextTask.Command = domain.CommandMVFromKeyframeVideoRecipe
			nextTask.Recipe = fc.Recipe
			nextTask.VideoRecipe = fc.VideoRecipe
			nextTask.CreatedAt = time.Now().UTC()
			if err := fc.TaskQueue.Enqueue(ctx, &nextTask); err != nil {
				return fmt.Errorf("enqueue continuation after cut %d: %w", cut.CutIndex, err)
			}
			return ErrPipelineDeferred
		}
	}
	domainRecipe, err := toDomainRecipe(fc.VideoRecipe)
	if err != nil {
		return err
	}
	fc.Recipe = domainRecipe
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
func hasPendingCuts(recipe *orchestrator.VideoRecipe) bool {
	if recipe == nil {
		return false
	}
	for i := range recipe.Cuts {
		if !recipe.Cuts[i].IsGenerated() {
			return true
		}
	}
	return false
}

// videoPrompt builds the prompt used for video generation.
func videoPrompt(cut orchestrator.Cut) string {
	anchor := strings.TrimSpace(cut.VisualAnchor)
	cue := strings.TrimSpace(cut.AudioCue)
	if cue == "" {
		return anchor
	}
	sync := "Synchronize motion and camera timing with audio cue: " + cue
	if anchor == "" {
		return sync
	}
	return anchor + "\n" + sync
}
