// Package step は、動画生成ワーカーのパイプラインを構成する各ステップ（Step）の
// 実装を提供します。
package step

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

const maxRecipeJSONSize = 5 * 1024 * 1024

// RecipeLoadStep は、タスクからレシピ（VideoRecipe/MusicRecipe）を読み込む
// パイプラインの最初のステップです。
type RecipeLoadStep struct{}

// Name returns the receiver name.
func (RecipeLoadStep) Name() string { return "recipe_load" }

// Execute runs the receiver processing step.
func (RecipeLoadStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil || sc.Task == nil {
		return fmt.Errorf("recipe load requires task")
	}
	if sc.VideoRecipe == nil {
		sc.VideoRecipe = sc.Task.VideoRecipe
	}
	// レシピの入手経路は3通り（既存の VideoRecipe / ストレージからデコードした
	// VideoRecipe / MusicRecipe からの変換）だが、いずれも最終的に sc.VideoRecipe を
	// 確定させたうえで、末尾で一度だけタスク由来の audio_url / character_id を適用する。
	switch {
	case sc.VideoRecipe != nil:
		// 既に VideoRecipe を持っている。ドメインレシピが未設定なら派生させる。
		if sc.Recipe == nil {
			recipe, err := toDomainRecipe(sc.VideoRecipe)
			if err != nil {
				return err
			}
			sc.Recipe = recipe
		}
	case sc.Recipe == nil:
		// VideoRecipe も MusicRecipe も無いので RecipeURL から読み込む。
		if strings.TrimSpace(sc.Task.RecipeURL) == "" {
			return fmt.Errorf("recipe or recipe_url is required")
		}
		if sc.Reader == nil {
			return fmt.Errorf("recipe reader is not configured")
		}
		recipe, videoRecipe, err := readRecipeInput(ctx, sc.Reader, sc.Task.RecipeURL)
		if err != nil {
			return err
		}
		if videoRecipe != nil {
			sc.VideoRecipe = videoRecipe
			if recipe == nil {
				recipe, err = toDomainRecipe(sc.VideoRecipe)
				if err != nil {
					return err
				}
			}
			sc.Recipe = recipe
		} else {
			// MusicRecipe をデコードした。下の変換ブロックで VideoRecipe を組む。
			sc.Recipe = recipe
		}
	}
	// ここまでで VideoRecipe が未確定なら（MusicRecipe 経路）変換して確定させる。
	if sc.VideoRecipe == nil {
		if err := domain.NormalizeMusicRecipe(sc.Recipe); err != nil {
			return err
		}
		recipe, err := toVideoRecipe(sc.Recipe)
		if err != nil {
			return err
		}
		sc.VideoRecipe = recipe
	}
	applyTaskAudioURLToVideoRecipe(sc.Task, sc.VideoRecipe)
	applyTaskCharacterIDToVideoRecipe(sc.Task, sc.VideoRecipe)
	absolutizeKeyframeReferences(sc.VideoRecipe, originalJobOutputPath(sc.Task.RecipeURL))
	return nil
}

// absolutizeKeyframeReferences は、元ジョブ相対で保存された keyframe_reference を
// そのジョブのルートで絶対 URI 化します。
//
// 保存済みレシピは新しいジョブとして実行されるため、相対参照のままだと新ジョブの出力パスを
// 基準に解決されて別のオブジェクト（たいていは存在しないもの）を指します。ここで直しておくのは、
// 下流の CutKeyframeRunner が「keyframe_reference が空でない = 焼き直さない」で判断するためです。
// 解決前に渡すと、指し先の壊れた参照をそのまま再利用してしまいます。
//
// base が空（レシピ JSON を直接渡された場合など）や、すでにスキーム付きの参照はそのままです。
func absolutizeKeyframeReferences(recipe *video.Recipe, base string) {
	if recipe == nil || base == "" {
		return
	}
	for i := range recipe.Cuts {
		recipe.Cuts[i].KeyframeReference = resolveRecipeObjectURI(base, recipe.Cuts[i].KeyframeReference)
	}
}

// readRecipeInput reads and validates a music recipe or keyframe video recipe from remote storage.
func readRecipeInput(ctx context.Context, reader interface {
	Open(context.Context, string) (io.ReadCloser, error)
}, uri string) (*domain.MusicRecipe, *domain.VideoRecipe, error) {
	rc, err := reader.Open(ctx, strings.TrimSpace(uri))
	if err != nil {
		return nil, nil, fmt.Errorf("read recipe url: %w", err)
	}
	defer rc.Close()

	raw, err := io.ReadAll(io.LimitReader(rc, maxRecipeJSONSize))
	if err != nil {
		return nil, nil, fmt.Errorf("read recipe json: %w", err)
	}
	return domain.UnmarshalRecipeOrVideoRecipe(raw)
}
