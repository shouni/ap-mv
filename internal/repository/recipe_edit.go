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

// GetRecipe returns the job's VideoRecipe as-is.
//
// It is a separate path from GetHistory, which formats for display. In a read-edit-write round
// trip, display-only values such as signed URLs or estimated costs mixed in could not be undone once saved.
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

// SaveRecipe overwrites the job's VideoRecipe.
//
// It is the path that lets the cut list of a script-only job be fixed at zero image cost. If the
// only way to apply edits were recipe_json at generation time, a review would end after one round,
// and a save meant for "check, then render" would be read-only.
//
// It runs Normalize and validation before saving for the same reason as RecipeSaveFilter (not
// to leave a recipe that is listed but fails in keyframe generation).
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
