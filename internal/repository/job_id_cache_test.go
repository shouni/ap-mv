package repository

import (
	"context"
	"slices"
	"testing"
)

const testBaseURI = "gs://bucket/ap-mv/veo/jobs"
const testListPrefix = testBaseURI + "/"

func jobListReaderFixture() *fakeHistoryReader {
	return &fakeHistoryReader{
		paths: []string{
			"gs://bucket/ap-mv/veo/jobs/job-20260501-123456-aaaa/video_music_meta.json",
			"gs://bucket/ap-mv/veo/jobs/job-20260502-123456-bbbb/video_music_meta.json",
		},
		files: map[string]string{},
	}
}

// 一覧は区切り文字付きの List に乗るため、1 ジョブが成果物の数だけ重複して現れず、
// ジョブの疑似ディレクトリ 1 件として集まること。カットごとのキーフレーム・動画で
// 1 ジョブが数百オブジェクトになるため、ここが崩れると一覧走査がその倍数で効きます。
func TestCollectJobIDsFoldsJobArtifactsIntoOneEntry(t *testing.T) {
	t.Parallel()

	reader := &fakeHistoryReader{
		paths: []string{
			"gs://bucket/ap-mv/veo/jobs/job-20260501-123456-aaaa/video_music_meta.json",
			"gs://bucket/ap-mv/veo/jobs/job-20260501-123456-aaaa/cuts/cut-1.mp4",
			"gs://bucket/ap-mv/veo/jobs/job-20260501-123456-aaaa/regens/cut-2/keyframe.png",
			"gs://bucket/ap-mv/veo/jobs/job-20260502-123456-bbbb/video_music_meta.json",
			// 再生成用の作業ジョブは一覧に出さない
			"gs://bucket/ap-mv/veo/jobs/regen-keyframe-20260503-123456-cccc/video_music_meta.json",
			// ジョブディレクトリではない直下のオブジェクト
			"gs://bucket/ap-mv/veo/jobs/stray.json",
		},
		files: map[string]string{},
	}
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: testBaseURI, Store: reader, HistoryCache: NewHistoryCache()})

	jobIDs, err := repo.collectJobIDs(context.Background())
	if err != nil {
		t.Fatalf("collectJobIDs() error = %v", err)
	}

	want := []string{"job-20260501-123456-aaaa", "job-20260502-123456-bbbb"}
	if !slices.Equal(jobIDs, want) {
		t.Errorf("collectJobIDs() = %v, want %v", jobIDs, want)
	}
}

// 履歴一覧は baseURI 配下の全走査になるため、ジョブ ID 一覧はキャッシュされ、
// 2 回目以降のページ表示で List が再実行されないこと。
func TestListHistoryPageCachesJobIDList(t *testing.T) {
	t.Parallel()

	reader := jobListReaderFixture()
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: testBaseURI, Store: reader, HistoryCache: NewHistoryCache()})

	for range 3 {
		if _, err := repo.ListHistoryPage(context.Background(), 1, 10, ""); err != nil {
			t.Fatalf("ListHistoryPage() error = %v", err)
		}
	}

	if got := reader.ListCount(testListPrefix); got != 1 {
		t.Fatalf("list count = %d, want 1 (ジョブ ID 一覧がキャッシュされていない)", got)
	}
}

// 削除は TTL の満了を待たずに一覧へ反映される必要がある。
func TestDeleteHistoryInvalidatesJobIDList(t *testing.T) {
	t.Parallel()

	reader := jobListReaderFixture()
	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: testBaseURI, Store: reader, HistoryCache: NewHistoryCache()})

	if _, err := repo.ListHistoryPage(context.Background(), 1, 10, ""); err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}
	if err := repo.DeleteHistory(context.Background(), "job-20260501-123456-aaaa"); err != nil {
		t.Fatalf("DeleteHistory() error = %v", err)
	}
	if _, err := repo.ListHistoryPage(context.Background(), 1, 10, ""); err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}

	if got := reader.ListCount(testListPrefix); got != 2 {
		t.Fatalf("list count = %d, want 2 (削除後に一覧キャッシュが破棄されていない)", got)
	}
}

// キャッシュされたジョブ ID 一覧はページ切り出し時にその場でソートされるため、
// 複製を返さないと並行アクセスで競合する。-race 付きで検出させる。
func TestListJobIDsIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	repo := NewVideoHistoryRepository(VideoHistoryRepositoryConfig{BaseURI: testBaseURI, Store: jobListReaderFixture(), HistoryCache: NewHistoryCache()})
	if _, err := repo.ListHistoryPage(context.Background(), 1, 10, ""); err != nil {
		t.Fatalf("ListHistoryPage() error = %v", err)
	}

	done := make(chan struct{})
	for range 4 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 20 {
				if _, err := repo.ListHistoryPage(context.Background(), 1, 1, ""); err != nil {
					t.Errorf("ListHistoryPage() error = %v", err)
					return
				}
			}
		}()
	}
	for range 4 {
		<-done
	}
}
