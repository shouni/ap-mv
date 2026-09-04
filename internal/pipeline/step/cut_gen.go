package step

import (
	"context"
	"fmt"
	"strings"

	"github.com/shouni/ap-mv/internal/domain"
)

// CutKeyframeStep は、各カットのキーフレーム画像を生成するパイプラインステップです。
type CutKeyframeStep struct{}

// Name returns the receiver name.
func (CutKeyframeStep) Name() string { return "cut_keyframe_gen" }

// Execute runs the receiver processing step.
func (CutKeyframeStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil {
		return fmt.Errorf("cut keyframe generation requires recipe")
	}
	if err := ensureVideoRecipe(sc); err != nil {
		return err
	}
	applyTaskAudioURLToVideoRecipe(sc.Task, sc.VideoRecipe)
	applyTaskCharacterIDToVideoRecipe(sc.Task, sc.VideoRecipe)
	// RunAndSave（下）はキーフレーム生成後、自らGCSへvideo_music_meta.jsonを書き込む
	// （go-veo-orchestrator/runner/keyframe.go）。そのため呼び出し前にAspectRatioを
	// 確定させておく必要がある。呼び出し後に設定すると、メモリ上のオブジェクトは
	// 更新されてもGCS上のファイルには反映されない（既に書き込み済みのため）。
	if strings.TrimSpace(sc.VideoRecipe.AspectRatio) == "" {
		sc.VideoRecipe.AspectRatio = resolvedAspectRatio(sc.Task)
	}
	if sc.Workflows == nil || sc.Workflows.CutKeyframe == nil {
		recipe, err := toDomainRecipe(sc.VideoRecipe)
		sc.Recipe = recipe
		return err
	}
	outputPath := sc.OutputPath
	if outputPath == "" {
		return fmt.Errorf("output path is required")
	}
	// RunAndSave は keyframe_reference を持つカットを焼き直しません
	// （go-veo-orchestrator v1.10.0 以降）。SceneSplitStep が 1:1 で再割り当てされた
	// カットの参照を残すため、保存済みレシピからの動画生成は画像を作り直さずに済みます。
	recipe, err := sc.Workflows.CutKeyframe.GenerateAndSave(ctx, sc.VideoRecipe, outputPath)
	if err != nil {
		return err
	}
	applyLyricsToVideoRecipeCuts(recipe)
	sc.VideoRecipe = recipe
	sc.Recipe, err = toDomainRecipe(recipe)
	return err
}

// resolvedAspectRatio returns the aspect ratio a keyframe generation task actually used: the
// task's explicit choice (set once at recipe creation, or inherited from an existing recipe for
// operations on an existing job — see PostVideoRecipeCreate / PostGenerateVideoFromHistory /
// PostRegenerateCutKeyframe), falling back to domain.DefaultAspectRatio when the task has none.
// The kit holds no art-direction default any more, so this app owns the fallback. Recording it
// on the recipe lets later video generation steps read a single source of truth instead of
// choosing independently.
func resolvedAspectRatio(task *domain.Task) string {
	if task != nil {
		if aspectRatio := strings.TrimSpace(task.VeoAspectRatio); aspectRatio != "" {
			return aspectRatio
		}
	}
	return domain.DefaultAspectRatio
}
