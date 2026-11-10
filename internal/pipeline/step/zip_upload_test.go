package step

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

// zipTestReader is a fake that returns bytes for a KeyframeReference URI.
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
					// A cut without a keyframe is not included in the ZIP.
					CutIndex:    2,
					DurationSec: 8, StartSec: 8, EndSec: 16,
				},
			},
		},
	}
}

// TestZipUploadBuildsArchive checks that the ZIP holds "keyframe images + inputs.txt +
// subtitles.ass". It runs the io.Pipe + goroutine structure (including handing over writeErr)
// end to end; this whole filter used to have 0% coverage.
func TestZipUploadBuildsArchive(t *testing.T) {
	writer := newMemoryWriter()
	reader := &zipTestReader{files: map[string]string{
		"gs://bucket/images/keyframe_1.png": "png-bytes",
	}}
	sc := zipTestContext(writer, reader)

	if err := (ZipUploadStep{}).Execute(context.Background(), sc); err != nil {
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

// TestZipUploadPropagatesReadFailure checks that a keyframe read failure comes back as an error
// through the Writer (a regression test for the structure where writeErr in the goroutine
// propagates through the pipe's CloseWithError).
func TestZipUploadPropagatesReadFailure(t *testing.T) {
	writer := newMemoryWriter()
	reader := &zipTestReader{err: errors.New("gcs unavailable")}
	sc := zipTestContext(writer, reader)

	err := (ZipUploadStep{}).Execute(context.Background(), sc)
	if err == nil {
		t.Fatal("Execute() must fail when a keyframe cannot be read")
	}
	if !strings.Contains(err.Error(), "gcs unavailable") {
		t.Fatalf("error should carry the read failure: %v", err)
	}
}

// TestZipUploadSkipsRegenWithoutOverwrite checks that the keyframe regeneration command does not
// build a ZIP when OverwriteKeyframe is off (a guard against unintentionally overwriting the
// original job's keyframes.zip).
func TestZipUploadSkipsRegenWithoutOverwrite(t *testing.T) {
	writer := newMemoryWriter()
	sc := zipTestContext(writer, &zipTestReader{})
	sc.Task.Command = domain.CommandRegenerateCutKeyframe
	sc.Task.OverwriteKeyframe = false

	if err := (ZipUploadStep{}).Execute(context.Background(), sc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(writer.objects) != 0 {
		t.Errorf("zip must not be written for regen without overwrite: %v", len(writer.objects))
	}
}
