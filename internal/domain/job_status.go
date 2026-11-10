package domain

import (
	"strings"
	"time"

	"github.com/shouni/gcp-kit/jobstatus"
)

// JobState is the job's state in its lifecycle.
// It is gcp-kit's jobstatus.State.
type JobState = jobstatus.State

const (
	// JobStateQueued means enqueued to Cloud Tasks, with no worker processing it yet.
	JobStateQueued = jobstatus.StateQueued
	// JobStateRunning means a worker is processing it.
	// For video generation split per cut, the job stays in this state while handed over to continuation tasks.
	JobStateRunning = jobstatus.StateRunning
	// JobStateSucceeded means everything through publishing the artifacts has finished.
	JobStateSucceeded = jobstatus.StateSucceeded
	// JobStateFailed means processing failed. It may be subject to retry by Cloud Tasks.
	JobStateFailed = jobstatus.StateFailed
)

// ErrJobStatusNotFound means no job status has been recorded yet.
// "No status" is a normal state, not an anomaly (a submission before recording, or a job made
// before this feature), so it is a separate error the caller can tell from a storage failure.
//
// Like JobState, it points straight at gcp-kit's value. Aliasing only the state constants in
// domain while keeping the error in repository had split the references for the same job-status
// surface into "states via domain, errors via the concrete package".
var ErrJobStatusNotFound = jobstatus.ErrNotFound

// ErrJobStatusUnavailable means the job status "should exist but could not be read".
// Mixing it with not-recorded would mistake a finished job for an unfinished one and redo the whole generation.
var ErrJobStatusUnavailable = jobstatus.ErrUnavailable

// JobStatus is the job's progress.
//
// Whether generation succeeded used to be left only in Slack notices, and failed jobs vanished
// from the UI entirely. With this record, both the UI and M2M clients can follow a job's state
// after submission. It is also the basis of the re-run guard against Cloud Tasks' at-least-once delivery.
//
// The common fields (JobID, State, Attempts, etc.) and IsTerminal belong to jobstatus.Status.
// It is embedded, so both the Firestore document and the response JSON stay flat.
//
// Do not omit the firestore tags. Without them the stored field names become the Go identifiers
// (OriginalJobID) and disagree with the existing responses built from the json tags.
type JobStatus struct {
	jobstatus.Status
	// OriginalJobID is the ID of another job when the artifacts are written there
	// (keyframe regeneration, ZIP regeneration). The UI uses it to point at that job's history.
	OriginalJobID string `json:"original_job_id,omitempty" firestore:"original_job_id,omitempty"`
	// OutputURI is where the main artifact is stored on success.
	// Signed URLs expire, so they are not stored.
	OutputURI string `json:"output_uri,omitempty" firestore:"output_uri,omitempty"`

	// What follows is the history listing's headline. The listing used to open GCS's
	// video_music_meta.json for every row to build it. Keeping a copy makes the listing one query,
	// and filtering by progress stage works on the Firestore side. The cost of the copied values
	// going stale is paid by repainting them with ApplyVideoRecipe at every status record.
	Mood        string `json:"mood,omitempty" firestore:"mood,omitempty"`
	Tempo       int    `json:"tempo,omitzero" firestore:"tempo,omitempty"`
	VisualMode  string `json:"visual_mode,omitempty" firestore:"visual_mode,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty" firestore:"aspect_ratio,omitempty"`
	// FinalVideoURL is the gs:// URI of the finished video joining the continuation chain into one.
	FinalVideoURL string `json:"final_video_url,omitempty" firestore:"final_video_url,omitempty"`
	// GeneratedSeconds is the total duration of generated cuts (an estimate of Veo's billable seconds).
	// Multiplying by the price is the display side's job, so only the seconds are kept here.
	GeneratedSeconds float64 `json:"generated_seconds,omitzero" firestore:"generated_seconds,omitempty"`
	// Progress is the progress stage and the counts it is based on.
	//
	// Do not put omitempty on the firestore tag. With it, a job with no cuts at all would not have
	// the field written, and filtering on progress.stage could not pick up that document.
	Progress JobProgress `json:"progress" firestore:"progress"`
}

// ApplyVideoRecipe copies the recipe's contents into the listing headline. With no recipe it does nothing.
//
// It is called at submission, at the start of processing and at the terminal record. If any path
// skips it, the moment that record overwrites the whole state the headline alone disappears (a
// record carries over only the previous common fields).
func (s *JobStatus) ApplyVideoRecipe(recipe *VideoRecipe) {
	if recipe == nil {
		return
	}
	if title := strings.TrimSpace(firstNonEmptyString(recipe.MusicRecipe.Title, recipe.ProjectTitle)); title != "" {
		s.Title = title
	}
	s.Mood = strings.TrimSpace(recipe.MusicRecipe.Mood)
	s.Tempo = recipe.MusicRecipe.Tempo
	if mode := strings.TrimSpace(recipe.MusicRecipe.ComposeMode); mode != "" {
		s.VisualMode = mode
	}
	s.AspectRatio = strings.TrimSpace(recipe.AspectRatio)
	s.FinalVideoURL = strings.TrimSpace(recipe.FinalVideoURL)
	s.GeneratedSeconds = GeneratedSecondsOfCuts(recipe.Cuts)
	s.Progress = NewJobProgress(recipe.Cuts)
}

// firstNonEmptyString returns the first non-empty value.
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// NewJobStatus builds one status's headline from the task.
//
// Submission (web) and each transition record (worker) use the same construction. Written
// separately, the worker's record overwrites the whole state while copying fewer fields than
// the web side, so a headline filled only at submission disappears the moment running is
// recorded. Adding a field to one side still compiles, so it is noticed only when the listing goes blank.
func NewJobStatus(task *Task, state JobState) JobStatus {
	if task == nil {
		return JobStatus{}
	}

	status := JobStatus{
		JobID:         task.JobID,
		Command:       string(task.ListedCommand()),
		State:         state,
		OriginalJobID: task.OriginalJobID,
		VisualMode:    strings.TrimSpace(task.VisualMode),
	}
	if task.Recipe != nil {
		status.Title = strings.TrimSpace(task.Recipe.Title)
	}
	status.ApplyVideoRecipe(task.VideoRecipe)
	return status
}

// NewQueuedJobStatus builds the job status right after enqueueing.
//
// The listing headline is filled here too. Without what can be copied from the recipe at
// submission, not even the title shows in the listing until generation finishes.
func NewQueuedJobStatus(task *Task, now time.Time) JobStatus {
	status := NewJobStatus(task, JobStateQueued)
	// QueuedAt is stamped by Store on save (now when unset).
	status.UpdatedAt = now
	return status
}
