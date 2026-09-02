package repository

import (
	"context"
	"sync"

	"github.com/shouni/gcp-kit/jobstatus"

	"github.com/shouni/ap-mv/internal/domain"
)

// fakeStatusStore は ports.JobStatusStore のフェイクです。
//
// 絞り込みも並べ替えもここでは行いません。ListOption は Firestore へ渡す不透明な値で、
// 外から中身を見られないためです。それらの担保はエミュレータと ap-infra の索引の側にあり、
// ここで確かめるのは状態から一覧 1 行への変換と、ページ番号・オプション数の受け渡しです。
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
