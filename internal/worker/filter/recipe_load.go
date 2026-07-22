// Package filter は、動画生成ワーカーのパイプラインを構成する各ステップ（Filter）の
// 実装を提供します。
package filter

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/shouni/ap-mv/internal/domain"
)

const maxRecipeJSONSize = 5 * 1024 * 1024

// RecipeLoadFilter は、タスクからレシピ（VideoRecipe/MusicRecipe）を読み込む
// パイプラインの最初のステップです。
type RecipeLoadFilter struct{}

// Name returns the receiver name.
func (RecipeLoadFilter) Name() string { return "recipe_load" }

// Execute runs the receiver processing step.
func (RecipeLoadFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil || fc.Task == nil {
		return fmt.Errorf("recipe load requires task")
	}
	if fc.VideoRecipe == nil {
		fc.VideoRecipe = fc.Task.VideoRecipe
	}
	// レシピの入手経路は3通り（既存の VideoRecipe / ストレージからデコードした
	// VideoRecipe / MusicRecipe からの変換）だが、いずれも最終的に fc.VideoRecipe を
	// 確定させたうえで、末尾で一度だけタスク由来の audio_url / character_id を適用する。
	switch {
	case fc.VideoRecipe != nil:
		// 既に VideoRecipe を持っている。ドメインレシピが未設定なら派生させる。
		if fc.Recipe == nil {
			recipe, err := toDomainRecipe(fc.VideoRecipe)
			if err != nil {
				return err
			}
			fc.Recipe = recipe
		}
	case fc.Recipe == nil:
		// VideoRecipe も MusicRecipe も無いので RecipeURL から読み込む。
		if strings.TrimSpace(fc.Task.RecipeURL) == "" {
			return fmt.Errorf("recipe or recipe_url is required")
		}
		if fc.Reader == nil {
			return fmt.Errorf("recipe reader is not configured")
		}
		recipe, videoRecipe, err := readRecipeInput(ctx, fc.Reader, fc.Task.RecipeURL)
		if err != nil {
			return err
		}
		if videoRecipe != nil {
			fc.VideoRecipe = videoRecipe
			if recipe == nil {
				recipe, err = toDomainRecipe(fc.VideoRecipe)
				if err != nil {
					return err
				}
			}
			fc.Recipe = recipe
		} else {
			// MusicRecipe をデコードした。下の変換ブロックで VideoRecipe を組む。
			fc.Recipe = recipe
		}
	}
	// ここまでで VideoRecipe が未確定なら（MusicRecipe 経路）変換して確定させる。
	if fc.VideoRecipe == nil {
		if err := domain.NormalizeMusicRecipe(fc.Recipe); err != nil {
			return err
		}
		recipe, err := toVideoRecipe(fc.Recipe)
		if err != nil {
			return err
		}
		fc.VideoRecipe = recipe
	}
	applyTaskAudioURLToVideoRecipe(fc.Task, fc.VideoRecipe)
	applyTaskCharacterIDToVideoRecipe(fc.Task, fc.VideoRecipe)
	return nil
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
