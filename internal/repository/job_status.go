package repository

import (
	"cloud.google.com/go/firestore"
	"github.com/shouni/gcp-kit/jobstatus"

	"github.com/shouni/ap-mv/internal/domain"
)

// statusCollection is the Firestore collection for job status.
//
// It uses the same vocabulary as the artifact bucket. There is one collection per service; the
// shape of one shared collection with a discriminator field is not used (forgetting to filter
// would not fail and would mix other services' jobs into history). It is a constant rather than a
// setting because it is the service's identity, not a per-deployment value.
const statusCollection = "ap-mv"

// NewJobStatusRepository builds the Firestore-backed reads and writes of job progress.
//
// The storage format, job ID normalisation and error classification belong to gcp-kit's jobstatus.
// ports.JobStatusStore has the same signature as jobstatus.Store, so no wrapper type is needed.
// A document always holds only the latest generation and is updated by overwriting.
//
// The status lives apart from the artifacts, so deleting history (bulk prefix delete) does not
// remove it. Removing the document is the handler's job (handlers.deleteJobStatus).
func NewJobStatusRepository(client *firestore.Client) *jobstatus.Store[domain.JobStatus] {
	return jobstatus.NewStore[domain.JobStatus](client, statusCollection)
}

// Recorder.Begin finds by type that Store satisfies Modifier and does the re-run guard and the
// running record in one Firestore transaction. Wrapping Store or downgrading gcp-kit below
// v1.21.0 stops compilation here. Running without satisfying it, the guard would silently fall
// back to a path where Get and Save are separate (and can interleave).
var _ jobstatus.Modifier[domain.JobStatus] = (*jobstatus.Store[domain.JobStatus])(nil)
