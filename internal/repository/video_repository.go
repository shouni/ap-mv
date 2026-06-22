package repository

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"ap-mv/internal/domain"
	"ap-mv/internal/ports"
)

// DownloadKeyframes はジョブのキーフレーム画像を1枚ずつ sink へストリーミングします。
// キーフレームが存在するカットのみ対象で、ファイル名は cut_01.png 形式です。
func (r *VideoHistoryRepository) DownloadKeyframes(ctx context.Context, jobID string, sink ports.KeyframeSink) error {
	if r == nil || r.reader == nil || r.baseURI == "" {
		return errors.New("history repository is not properly configured")
	}
	if err := domain.ValidateJobID(jobID); err != nil {
		return err
	}
	recipe, err := r.loadVideoRecipe(ctx, jobID)
	if err != nil {
		return err
	}
	for _, cut := range recipe.Cuts {
		ref := strings.TrimSpace(cut.KeyframeReference)
		if ref == "" {
			continue
		}
		uri := r.resolveJobObjectURI(jobID, ref)
		ext := path.Ext(uri)
		if ext == "" {
			ext = ".png"
		}
		name := fmt.Sprintf("cut_%02d%s", cut.CutIndex, ext)
		if err := r.streamKeyframe(ctx, uri, cut.CutIndex, name, sink); err != nil {
			return err
		}
	}
	return nil
}

// streamKeyframe は1カット分のキーフレームを読み取り sink へ渡します。
// defer で rc を確実に Close するためにループ本体から切り出しています。
func (r *VideoHistoryRepository) streamKeyframe(ctx context.Context, uri string, cutIndex int, name string, sink ports.KeyframeSink) error {
	rc, err := r.reader.Open(ctx, uri)
	if err != nil {
		return fmt.Errorf("open keyframe for cut %d: %w", cutIndex, err)
	}
	defer rc.Close()
	if err := sink(name, rc); err != nil {
		return fmt.Errorf("write keyframe for cut %d: %w", cutIndex, err)
	}
	return nil
}

// DeleteHistory deletes all stored objects under a generated MV job directory.
func (r *VideoHistoryRepository) DeleteHistory(ctx context.Context, jobID string) error {
	if r == nil || r.reader == nil || r.writer == nil || r.baseURI == "" {
		return nil
	}
	if err := domain.ValidateJobID(jobID); err != nil {
		return err
	}
	prefix := r.baseURI + "/" + jobID + "/"
	var paths []string
	if err := r.reader.List(ctx, prefix, func(gcsPath string) error {
		paths = append(paths, gcsPath)
		return nil
	}); err != nil {
		return fmt.Errorf("list history objects for deletion: %w", err)
	}
	if len(paths) == 0 {
		paths = append(paths, r.metadataURI(jobID))
	}

	var errs []error
	for _, p := range paths {
		if err := r.writer.Delete(ctx, p); err != nil {
			errs = append(errs, fmt.Errorf("delete %s: %w", p, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	r.deleteCachedHistory(jobID)
	r.deleteCachedVideoRecipe(jobID)
	return nil
}
