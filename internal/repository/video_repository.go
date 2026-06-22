package repository

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"ap-mv/internal/domain"
)

// DownloadKeyframes reads all keyframe images for the given job and returns them as KeyframeFile entries.
// Only cuts with a valid KeyframeReference are included. Files are named cut_01.png, cut_02.png, etc.
func (r *VideoHistoryRepository) DownloadKeyframes(ctx context.Context, jobID string) ([]domain.KeyframeFile, error) {
	if r == nil || r.reader == nil || r.baseURI == "" {
		return nil, errors.New("history repository is not properly configured")
	}
	if err := domain.ValidateJobID(jobID); err != nil {
		return nil, err
	}
	recipe, err := r.loadVideoRecipe(ctx, jobID)
	if err != nil {
		return nil, err
	}
	var files []domain.KeyframeFile
	for _, cut := range recipe.Cuts {
		ref := strings.TrimSpace(cut.KeyframeReference)
		if ref == "" {
			continue
		}
		uri := r.resolveJobObjectURI(jobID, ref)
		rc, err := r.reader.Open(ctx, uri)
		if err != nil {
			return nil, fmt.Errorf("open keyframe for cut %d: %w", cut.CutIndex, err)
		}
		data, readErr := io.ReadAll(rc)
		rc.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read keyframe for cut %d: %w", cut.CutIndex, readErr)
		}
		ext := path.Ext(uri)
		if ext == "" {
			ext = ".png"
		}
		files = append(files, domain.KeyframeFile{
			Name:     fmt.Sprintf("cut_%02d%s", cut.CutIndex, ext),
			Data:     data,
			MimeType: mimeTypeFromExt(ext),
		})
	}
	return files, nil
}

func mimeTypeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	default:
		return "image/png"
	}
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
