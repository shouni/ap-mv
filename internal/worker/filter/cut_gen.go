package filter

import (
	"context"
	"fmt"
	"strings"

	"github.com/shouni/go-veo-orchestrator/keyframe"

	"github.com/shouni/ap-mv/internal/domain"
)

// CutKeyframeFilter は、各カットのキーフレーム画像を生成するパイプラインステップです。
type CutKeyframeFilter struct{}

// Name returns the receiver name.
func (CutKeyframeFilter) Name() string { return "cut_keyframe_gen" }

// Execute runs the receiver processing step.
func (CutKeyframeFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil {
		return fmt.Errorf("cut keyframe generation requires recipe")
	}
	if err := ensureVideoRecipe(fc); err != nil {
		return err
	}
	applyTaskAudioURLToVideoRecipe(fc.Task, fc.VideoRecipe)
	applyTaskCharacterIDToVideoRecipe(fc.Task, fc.VideoRecipe)
	// RunAndSave（下）はキーフレーム生成後、自らGCSへvideo_music_meta.jsonを書き込む
	// （go-veo-orchestrator/runner/keyframe.go）。そのため呼び出し前にAspectRatioを
	// 確定させておく必要がある。呼び出し後に設定すると、メモリ上のオブジェクトは
	// 更新されてもGCS上のファイルには反映されない（既に書き込み済みのため）。
	if strings.TrimSpace(fc.VideoRecipe.AspectRatio) == "" {
		fc.VideoRecipe.AspectRatio = resolvedAspectRatio(fc.Task)
	}
	if fc.Workflows == nil || fc.Workflows.CutKeyframe == nil {
		recipe, err := toDomainRecipe(fc.VideoRecipe)
		fc.Recipe = recipe
		return err
	}
	outputPath := fc.OutputPath
	if outputPath == "" {
		return fmt.Errorf("output path is required")
	}
	if allCutsHaveKeyframes(fc.VideoRecipe.Cuts) {
		return reuseExistingKeyframes(ctx, fc, outputPath)
	}
	recipe, err := fc.Workflows.CutKeyframe.RunAndSave(ctx, fc.VideoRecipe, outputPath)
	if err != nil {
		return err
	}
	applyLyricsToVideoRecipeCuts(recipe)
	fc.VideoRecipe = recipe
	fc.Recipe, err = toDomainRecipe(recipe)
	return err
}

// allCutsHaveKeyframes reports whether every cut already points at a generated keyframe image.
//
// CutKeyframeRunner.RunAndSave has no per-cut skip — it regenerates the whole recipe — so
// without this check, "generate a video from the saved keyframes" would re-bake every image
// first. SceneSplitFilter keeps a cut's KeyframeReference when the cut re-allocates 1:1, which
// is what makes this reachable for an already-planned recipe.
//
// 相対パスの参照（旧ジョブのレシピ）は元ジョブのベースからしか解決できず、このジョブの
// 出力パスとは一致しないため温存の対象にしません（従来どおり焼き直します）。
func allCutsHaveKeyframes(cuts []domain.VideoCut) bool {
	if len(cuts) == 0 {
		return false
	}
	for _, cut := range cuts {
		if !strings.Contains(strings.TrimSpace(cut.KeyframeReference), "://") {
			return false
		}
	}
	return true
}

// reuseExistingKeyframes finishes the keyframe stage without generating any image.
//
// RunAndSave が副産物として書いていた video_music_meta.json をここで明示的に書きます。
// 履歴一覧はこのファイルを目印にジョブを拾うため、書かずに進むと生成中のジョブが
// 履歴から見えなくなります（フルMVは数十分かかるので、その間ずっと消えます）。
func reuseExistingKeyframes(ctx context.Context, fc *Context, outputPath string) error {
	applyLyricsToVideoRecipeCuts(fc.VideoRecipe)
	if fc.Workflows.Publish != nil {
		if _, err := fc.Workflows.Publish.Run(ctx, fc.VideoRecipe, outputPath); err != nil {
			return fmt.Errorf("cut_keyframe_gen: save metadata for reused keyframes: %w", err)
		}
	}
	var err error
	fc.Recipe, err = toDomainRecipe(fc.VideoRecipe)
	return err
}

// resolvedAspectRatio returns the aspect ratio a keyframe generation task actually used: the
// task's explicit choice (set once at recipe creation, or inherited from an existing recipe for
// operations on an existing job — see PostVideoRecipeCreate / PostGenerateVideoFromHistory /
// PostRegenerateCutKeyframe), falling back to keyframe.CutAspectRatio (the same default the
// Generator itself applies) when the task has none. Recording this on the recipe lets later
// video generation steps read a single source of truth instead of choosing independently.
func resolvedAspectRatio(task *domain.Task) string {
	if task != nil {
		if aspectRatio := strings.TrimSpace(task.VeoAspectRatio); aspectRatio != "" {
			return aspectRatio
		}
	}
	return keyframe.CutAspectRatio
}
