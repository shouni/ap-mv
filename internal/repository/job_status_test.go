package repository

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-job-kit/jobstatus"

	"github.com/shouni/ap-mv/internal/domain"
)

// statusIO は書き込まれたオブジェクトをメモリに保持するテスト用の Reader/Writer です。
type statusIO struct {
	objects map[string]string
	written []string
}

func newStatusIO() *statusIO {
	return &statusIO{objects: map[string]string{}}
}

func (s *statusIO) Open(_ context.Context, path string) (io.ReadCloser, error) {
	body, ok := s.objects[path]
	if !ok {
		// remoteio は未存在を os.ErrNotExist に包んで返す。fake もそれに合わせないと
		// 「未存在」と「読めない」を取り違えたまま通ってしまう。
		return nil, fmt.Errorf("オブジェクトが見つかりません (%s): %w", path, os.ErrNotExist)
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func (s *statusIO) List(context.Context, string, func(string) error, ...remoteio.ListOption) error {
	return nil
}

func (s *statusIO) Exists(_ context.Context, path string) (bool, error) {
	_, ok := s.objects[path]
	return ok, nil
}

func (s *statusIO) Write(_ context.Context, path string, r io.Reader, _ ...remoteio.WriteOption) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.objects[path] = string(body)
	s.written = append(s.written, path)
	return nil
}

func (s *statusIO) Delete(_ context.Context, path string) error {
	delete(s.objects, path)
	return nil
}

func newStatusRepo(io *statusIO) *jobstatus.Store[domain.JobStatus] {
	return NewJobStatusRepository(testBaseURI, io, io)
}

// 状態はジョブ出力ディレクトリ配下に置き、履歴削除（プレフィックス一括削除）で
// 自動的に片付くようにする。履歴一覧は video_music_meta.json だけを拾うため混ざらない。
// 保存形式そのもの（未記録・破損 JSON・パストラバーサル・上書き）の検証は
// go-job-kit の jobstatus 側にあります。ここは ap-mv 固有の点だけを見ます。

func TestSaveWritesInsideJobDirectory(t *testing.T) {
	t.Parallel()

	io := newStatusIO()
	repo := newStatusRepo(io)

	err := repo.Save(context.Background(), "mv-20260726-123456-abcdef123456", domain.JobStatus{
		Status: jobstatus.Status{
			JobID:   "mv-20260726-123456-abcdef123456",
			Command: "mv_from_keyframe_video_recipe",
			State:   domain.JobStateQueued,
		},
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	want := testBaseURI + "/mv-20260726-123456-abcdef123456/status.json"
	if len(io.written) != 1 || io.written[0] != want {
		t.Fatalf("written = %v, want [%s]", io.written, want)
	}
}

func TestSaveAndGetRoundTrip(t *testing.T) {
	t.Parallel()

	repo := newStatusRepo(newStatusIO())
	original := domain.JobStatus{
		Status: jobstatus.Status{
			JobID:    "mv-20260726-123456-abcdef123456",
			Command:  "video_gen_continuation",
			State:    domain.JobStateSucceeded,
			Title:    "テスト曲",
			Attempts: 3,
		},
		OriginalJobID: "video-recipe-20260725-101010-aabbccdd",
		OutputURI:     testBaseURI + "/mv-20260726-123456-abcdef123456/",
	}
	if err := repo.Save(context.Background(), original.JobID, original); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := repo.Get(context.Background(), original.JobID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.State != domain.JobStateSucceeded {
		t.Fatalf("State = %q", got.State)
	}
	if got.Attempts != 3 {
		t.Fatalf("Attempts = %d", got.Attempts)
	}
	if got.OriginalJobID != original.OriginalJobID {
		t.Fatalf("OriginalJobID = %q", got.OriginalJobID)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt が設定されていない")
	}
}
