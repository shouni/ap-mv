package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"
)

// GetRecipe はジョブの VideoRecipe をそのまま返します。
//
// 表示用に整形する GetHistory とは別経路です。読んで直して書き戻す往復では、署名 URL や
// 概算コストのような表示専用の値が混ざると、そのまま保存したときに元へ戻せなくなります。
func (r *VideoHistoryRepository) GetRecipe(ctx context.Context, jobID string) (*domain.VideoRecipe, error) {
	if r == nil || r.store == nil || r.baseURI == "" {
		return nil, errors.New("history repository is not properly configured")
	}
	if err := jobid.Validate(jobID); err != nil {
		return nil, err
	}
	recipe, err := r.fetchVideoRecipe(ctx, jobID)
	if err != nil {
		return nil, err
	}
	return &recipe, nil
}

// SaveRecipe はジョブの VideoRecipe を上書き保存します。
//
// 台本だけのジョブのカット割りを画像コスト 0 で直せるようにするための経路です。直した
// 内容を反映する手段が生成時の recipe_json しか無いと、レビューは 1 周で終わってしまい、
// 「確認してから焼く」ための保存が読み取り専用になります。
//
// 保存前に Normalize と検証を通すのは RecipeSaveFilter と同じ理由です（一覧には載るが
// キーフレーム生成で落ちるレシピを残さない）。
func (r *VideoHistoryRepository) SaveRecipe(ctx context.Context, jobID string, recipe *domain.VideoRecipe) error {
	if r == nil || r.store == nil || r.baseURI == "" {
		return errors.New("history repository is not properly configured")
	}
	if err := jobid.Validate(jobID); err != nil {
		return err
	}
	if recipe == nil {
		return errors.New("recipe is nil")
	}
	recipe.Normalize()
	if err := domain.ValidateVideoRecipe(recipe); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(recipe, "", "  ")
	if err != nil {
		return fmt.Errorf("encode recipe: %w", err)
	}
	uri := r.metadataURI(jobID)
	if err := r.store.Write(ctx, uri, bytes.NewReader(raw), remoteio.WithContentType("application/json")); err != nil {
		return fmt.Errorf("write recipe (%s): %w", uri, err)
	}
	return nil
}
