package repository

import (
	"context"
	"errors"
	"fmt"

	"ap-mv/internal/domain"
)

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
	return nil
}
