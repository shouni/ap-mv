package ports

import (
	"context"

	"github.com/shouni/gcp-kit/jobstatus"

	"github.com/shouni/ap-mv/internal/domain"
)

// JobStatusStore persists job progress (queued/running/succeeded/failed) and serves the history
// listing's queries.
// The web process writes the state at submission and the worker process writes the run's result.
//
// The listing lives here because the history headline was copied into the status documents. It
// used to scan where artifacts are stored and open metadata one by one, so the listing was the
// artifacts' concern.
//
// The Save / Get / Delete signatures match gcp-kit's Store. So *jobstatus.Store[domain.JobStatus]
// is the implementation as-is and no adapter is needed to pass it to Recorder (the job ID used to
// be part of the state, and three services each kept an adapter just to make the shapes match).
type JobStatusStore interface {
	// Save stores the job status.
	Save(ctx context.Context, jobID string, status domain.JobStatus) error
	// Get fetches the job status. It returns an error when nothing is recorded.
	Get(ctx context.Context, jobID string) (domain.JobStatus, error)
	// Delete removes the job status.
	//
	// The status lives apart from the artifacts (in Firestore), so the bulk prefix delete of history
	// does not remove it. Without calling this, only the status of a job with no artifacts would remain.
	Delete(ctx context.Context, jobID string) error
	// List returns one page of job statuses, newest first.
	// The total count comes from an aggregation query, so documents outside the page are not read.
	List(ctx context.Context, page, perPage int, opts ...jobstatus.ListOption) ([]domain.JobStatus, domain.PageMeta, error)
}
