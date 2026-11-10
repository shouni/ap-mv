package pipeline

import (
	"context"

	"github.com/shouni/gcp-kit/jobstatus"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// statusRecorder is responsible for recording job status.
//
// The re-run guard, carrying over from the previous record and swallowing record failures are
// gathered in jobstatus.Recorder; all this supplies is building ap-mv's own state.
// The pipeline works even with no recorder configured; only the recording is skipped.
type statusRecorder struct {
	recorder *jobstatus.Recorder[domain.JobStatus]
}

// newStatusRecorder builds a statusRecorder backed by a ports.JobStatusStore.
// When store is nil, nothing is recorded.
//
// ports.JobStatusStore has the same signature as jobstatus.StatusStore, so it can be passed as-is.
func newStatusRecorder(store ports.JobStatusStore) statusRecorder {
	if store == nil {
		return statusRecorder{recorder: jobstatus.NewRecorder[domain.JobStatus](nil)}
	}
	return statusRecorder{recorder: jobstatus.NewRecorder[domain.JobStatus](store)}
}

// begin returns true when the job is already complete; otherwise it records the start of
// processing and returns false. The check and the record use one read of the previous record, so
// no other delivery can slip in between. The attempt count advances by one here.
//
// When the status cannot be read it returns an error and records nothing. The caller should
// return that error as-is and leave it to Cloud Tasks' redelivery. Falling to "unfinished" here
// would remake a finished job and incur the very cost this guard is meant to prevent; falling to
// "finished" would ACK an unfinished job's task and it would never run again.
func (s statusRecorder) begin(ctx context.Context, task *domain.Task) (bool, error) {
	if task == nil {
		return false, nil
	}
	// Continuation tasks carry the recipe in the payload, so the headline advances here.
	// Waiting for the terminal record would freeze the listing at its submission state while continuations repeat.
	return s.recorder.Begin(ctx, task.JobID, domain.NewJobStatus(task, domain.JobStateRunning),
		func(next, _ *domain.JobStatus) { next.Attempts++ },
	)
}

// markSucceeded records success and where the artifacts are stored.
//
// recipe is the recipe after the run. The listing headline is repainted here one last time. The
// recipe in the task payload is from before the run, so without passing this a finished job would
// stay one stage short in the listing.
func (s statusRecorder) markSucceeded(ctx context.Context, task *domain.Task, req domain.NotificationRequest, recipe *domain.VideoRecipe) {
	s.record(ctx, task, domain.JobStateSucceeded, func(next, _ *domain.JobStatus) {
		next.ApplyVideoRecipe(recipe)
		next.OutputURI = req.OutputURI
		if req.Title != "" {
			next.Title = req.Title
		}
	})
}

// markFailed records the failure and its reason.
//
// Even on failure the headline keeps what was reached (so a job that rendered its keyframes and
// failed at video does not look script-only in the listing).
func (s statusRecorder) markFailed(ctx context.Context, task *domain.Task, cause error, recipe *domain.VideoRecipe) {
	if cause == nil {
		return
	}
	s.record(ctx, task, domain.JobStateFailed, func(next, _ *domain.JobStatus) {
		next.ApplyVideoRecipe(recipe)
		next.Error = cause.Error()
	})
}

// record builds and saves the state for this record.
// Recorder carries Attempts, QueuedAt and, when no title could be taken from the recipe, Title
// over from the previous record. apply is called after that carry-over.
func (s statusRecorder) record(
	ctx context.Context,
	task *domain.Task,
	state domain.JobState,
	apply func(next, prev *domain.JobStatus),
) {
	if task == nil {
		return
	}
	s.recorder.Record(ctx, task.JobID, domain.NewJobStatus(task, state), apply)
}
