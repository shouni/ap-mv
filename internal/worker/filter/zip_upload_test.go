package filter

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

// zipTestReader は KeyframeReference の URI からバイト列を返すフェイクです。
type zipTestReader struct {
	files map[string]string
	err   error
}

func (r *zipTestReader) Open(_ context.Context, uri string) (io.ReadCloser, error) {
	if r.err != nil {
		return nil, r.err
	}
	data, ok := r.files[uri]
	if !ok {
		return nil, errors.New("object not found: " + uri)
	}
	return io.NopCloser(strings.NewReader(data)), nil
}

func zipTestContext(writer *memoryWriter, reader *zipTestReader) *Context {
	return &Context{
		Reader: reader,
		Writer: writer,
		Task: &domain.Task{
			JobID:   "video-recipe-20260618-081931-abcd1234",
			Command: domain.CommandVideoRecipeCreate,
		},
		OutputPath: "gs://bucket/jobs/video-recipe-20260618-081931-abcd1234/",
		VideoRecipe: &video.Recipe{
			ProjectTitle: "zip test",
			MusicRecipe: domain.MusicRecipe{
				Title: "zip test",
				Tempo: 120,
			},
			Cuts: []video.Cut{
				{
					CutIndex:    1,
					Dialogue:    "サビの歌詞",
					DurationSec: 8, StartSec: 0, EndSec: 8,
					KeyframeReference: "gs://bucket/images/keyframe_1.png",
				},
				{
					// キーフレームを持たないカットは ZIP に含まれない。
					CutIndex:    2,
					DurationSec: 8, StartSec: 8, EndSec: 16,
				},
			},
		},
	}
}

// TestZipUploadBuildsArchive は、ZIP に「キーフレーム画像 + inputs.txt + subtitles.ass」が
// 揃うことを検証します。io.Pipe + goroutine の構造（writeErr の受け渡し含む）を通しで
// 通すテストで、以前はこのフィルタ全体が 0% カバレッジでした。
func TestZipUploadBuildsArchive(t *testing.T) {
	writer := newMemoryWriter()
	reader := &zipTestReader{files: map[string]string{
		"gs://bucket/images/keyframe_1.png": "png-bytes",
	}}
	fc := zipTestContext(writer, reader)

	if err := (ZipUploadFilter{}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	zipURI := "gs://bucket/jobs/video-recipe-20260618-081931-abcd1234/keyframes.zip"
	raw, ok := writer.objects[zipURI]
	if !ok {
		t.Fatalf("zip was not written to %s (objects: %v)", zipURI, len(writer.objects))
	}

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("written object is not a valid zip: %v", err)
	}
	entries := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", f.Name, err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		entries[f.Name] = string(data)
	}

	if entries["cut_01.png"] != "png-bytes" {
		t.Errorf("cut_01.png = %q, want the keyframe bytes", entries["cut_01.png"])
	}
	if !strings.Contains(entries["inputs.txt"], "file 'cut_01.png'") {
		t.Errorf("inputs.txt missing keyframe entry: %q", entries["inputs.txt"])
	}
	if !strings.Contains(entries["subtitles.ass"], "サビの歌詞") && !strings.Contains(entries["subtitles.ass"], "Dialogue:") {
		t.Errorf("subtitles.ass missing karaoke line: %q", entries["subtitles.ass"])
	}
	if _, ok := entries["cut_02.png"]; ok {
		t.Error("cut without a keyframe must not appear in the zip")
	}
}

// TestZipUploadPropagatesReadFailure は、キーフレームの読み取り失敗が Writer 経由で
// エラーとして返ることを検証します（goroutine 内の writeErr が pipe の CloseWithError を
// 通じて伝播する構造の回帰テスト）。
func TestZipUploadPropagatesReadFailure(t *testing.T) {
	writer := newMemoryWriter()
	reader := &zipTestReader{err: errors.New("gcs unavailable")}
	fc := zipTestContext(writer, reader)

	err := (ZipUploadFilter{}).Execute(context.Background(), fc)
	if err == nil {
		t.Fatal("Execute() must fail when a keyframe cannot be read")
	}
	if !strings.Contains(err.Error(), "gcs unavailable") {
		t.Fatalf("error should carry the read failure: %v", err)
	}
}

// TestZipUploadSkipsRegenWithoutOverwrite は、キーフレーム再生成コマンドで
// OverwriteKeyframe が無効な場合に ZIP を作らないことを検証します（元ジョブの
// keyframes.zip を意図せず上書きしないためのガード）。
func TestZipUploadSkipsRegenWithoutOverwrite(t *testing.T) {
	writer := newMemoryWriter()
	fc := zipTestContext(writer, &zipTestReader{})
	fc.Task.Command = domain.CommandRegenerateCutKeyframe
	fc.Task.OverwriteKeyframe = false

	if err := (ZipUploadFilter{}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(writer.objects) != 0 {
		t.Errorf("zip must not be written for regen without overwrite: %v", len(writer.objects))
	}
}
