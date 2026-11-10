package repository

import (
	"context"
	"sync"

	"github.com/shouni/gcp-kit/jobstatus"

	"github.com/shouni/ap-mv/internal/domain"
)

// fakeStatusStore is a fake of ports.JobStatusStore.
//
// It does no filtering or sorting. ListOption is an opaque value passed to Firestore whose
// contents cannot be seen from outside. Those are guaranteed by the emulator and the indexes in
// the infrastructure repository; what this checks is the conversion from status to a listing row
// and passing the page number and option count through.
type fakeStatusStore struct {
	mu sync.Mutex

	statuses []domain.JobStatus

	listCalls   int
	lastPage    int
	lastPerPage int
	lastOptions int

	listErr error
}

func newFakeStatusStore(statuses ...domain.JobStatus) *fakeStatusStore {
	return &fakeStatusStore{statuses: statuses}
}

func (f *fakeStatusStore) Get(_ context.Context, jobID string) (domain.JobStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, status := range f.statuses {
		if status.JobID == jobID {
			return status, nil
		}
	}
	return domain.JobStatus{}, domain.ErrJobStatusNotFound
}

func (f *fakeStatusStore) Save(context.Context, string, domain.JobStatus) error { return nil }

func (f *fakeStatusStore) Delete(context.Context, string) error { return nil }

func (f *fakeStatusStore) List(_ context.Context, page, perPage int, opts ...jobstatus.ListOption) ([]domain.JobStatus, domain.PageMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.listCalls++
	f.lastPage = page
	f.lastPerPage = perPage
	f.lastOptions = len(opts)

	if f.listErr != nil {
		return nil, domain.PageMeta{}, f.listErr
	}
	return f.statuses, domain.PageMeta{Page: page, PerPage: perPage, Total: len(f.statuses)}, nil
}
