package filter

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/shouni/ap-mv/internal/domain"
)

// TestRecipeLoadFilterLoadsRecipeURLAndAppliesAudioURL verifies recipe loading and task audio URL application.
func TestRecipeLoadFilterLoadsRecipeURLAndAppliesAudioURL(t *testing.T) {
	reader := staticReader{content: `{
		"title": "recipe from gcs",
		"sections": [
			{"name": "intro", "duration_seconds": 8, "prompt": "blue light"}
		]
	}`}
	fc := &Context{
		Task: &domain.Task{
			JobID:     "job-1",
			Command:   domain.CommandMVFromKeyframeVideoRecipe,
			RecipeURL: "gs://bucket/recipe.json",
			AudioURL:  "gs://bucket/music.mp3",
		},
		Reader: reader,
	}

	if err := (RecipeLoadFilter{}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fc.Recipe == nil {
		t.Fatal("recipe was not loaded")
	}
	if fc.VideoRecipe == nil || len(fc.VideoRecipe.Cuts) == 0 {
		t.Fatal("video recipe cuts were not built")
	}
	if got := fc.VideoRecipe.Cuts[0].AudioReference; got != "gs://bucket/music.mp3" {
		t.Fatalf("AudioReference = %q, want audio URL", got)
	}
}

// TestRecipeLoadFilterLoadsVideoRecipeURL verifies keyframe VideoRecipe loading from GCS.
func TestRecipeLoadFilterLoadsVideoRecipeURL(t *testing.T) {
	reader := staticReader{content: `{
		"title": "video recipe from gcs",
		"cuts": [
			{"cut_index": 1, "duration_sec": 8, "visual_anchor": "blue light", "keyframe_reference": "gs://bucket/keyframe.png"}
		]
	}`}
	fc := &Context{
		Task: &domain.Task{
			JobID:     "job-1",
			Command:   domain.CommandMVFromKeyframeVideoRecipe,
			RecipeURL: "gs://bucket/video_music_meta.json",
		},
		Reader: reader,
	}

	if err := (RecipeLoadFilter{}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fc.VideoRecipe == nil || len(fc.VideoRecipe.Cuts) != 1 {
		t.Fatalf("video recipe was not loaded: %#v", fc.VideoRecipe)
	}
	if got := fc.VideoRecipe.Cuts[0].KeyframeReference; got != "gs://bucket/keyframe.png" {
		t.Fatalf("KeyframeReference = %q", got)
	}
	if fc.Recipe == nil {
		t.Fatal("domain recipe was not derived")
	}
}

// TestRecipeLoadFilterAbsolutizesRelativeKeyframes pins that job-relative keyframe paths from a
// stored recipe are resolved against the job the recipe came from, not the new job about to run.
//
// This matters because CutKeyframeRunner skips any cut whose KeyframeReference is non-empty. A
// relative path left as-is would satisfy that check while pointing at nothing under the new job's
// output path, so Veo would be handed a reference it cannot read.
func TestRecipeLoadFilterAbsolutizesRelativeKeyframes(t *testing.T) {
	reader := staticReader{content: `{
		"title": "legacy recipe",
		"cuts": [
			{"cut_index": 1, "duration_sec": 8, "visual_anchor": "a", "keyframe_reference": "images/keyframe_001.png"},
			{"cut_index": 2, "duration_sec": 8, "visual_anchor": "b", "keyframe_reference": "gs://bucket/other-job/images/keyframe_002.png"},
			{"cut_index": 3, "duration_sec": 8, "visual_anchor": "c"}
		]
	}`}
	fc := &Context{
		Task: &domain.Task{
			JobID:     "mv-2",
			Command:   domain.CommandMVFromKeyframeVideoRecipe,
			RecipeURL: "gs://bucket/ap-mv/veo/jobs/job-1/video_music_meta.json",
		},
		Reader: reader,
	}

	if err := (RecipeLoadFilter{}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []string{
		"gs://bucket/ap-mv/veo/jobs/job-1/images/keyframe_001.png",
		"gs://bucket/other-job/images/keyframe_002.png",
		"",
	}
	for i, expected := range want {
		if got := fc.VideoRecipe.Cuts[i].KeyframeReference; got != expected {
			t.Errorf("cut[%d].KeyframeReference = %q, want %q", i, got, expected)
		}
	}
}

// TestCutKeyframeFilterAppliesTaskCharacterID verifies that task character IDs are applied during keyframe generation.
func TestCutKeyframeFilterAppliesTaskCharacterID(t *testing.T) {
	fc := &Context{
		Task: &domain.Task{
			JobID:       "job-1",
			Command:     domain.CommandMVFromKeyframeVideoRecipe,
			CharacterID: "zundamon",
		},
		Recipe: &domain.MusicRecipe{
			Title: "recipe",
			Sections: []domain.MusicSection{
				{Name: "intro", Duration: 8, Prompt: "blue light"},
			},
		},
	}

	if err := (CutKeyframeFilter{}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fc.VideoRecipe == nil || len(fc.VideoRecipe.Cuts) == 0 {
		t.Fatal("video recipe cuts were not built")
	}
	if got := fc.VideoRecipe.Cuts[0].CharacterID; got != "zundamon" {
		t.Fatalf("CharacterID = %q, want zundamon", got)
	}
	if fc.Recipe == nil || len(fc.Recipe.Sections) == 0 {
		t.Fatal("music recipe sections were not retained")
	}
}

// TestVideoRecipeCreateDoesNotApplyTaskAudioURL verifies audio input is reserved for MV generation.
func TestVideoRecipeCreateDoesNotApplyTaskAudioURL(t *testing.T) {
	recipe := &domain.VideoRecipe{
		MusicRecipe: domain.MusicRecipe{Title: "recipe"},
		Cuts: []domain.VideoCut{
			{CutIndex: 1, VisualAnchor: "blue light", DurationSec: 8},
		},
	}
	task := &domain.Task{
		JobID:    "job-1",
		Command:  domain.CommandVideoRecipeCreate,
		AudioURL: "gs://bucket/music.mp3",
	}

	applyTaskAudioURLToVideoRecipe(task, recipe)

	if got := recipe.Cuts[0].AudioReference; got != "" {
		t.Fatalf("AudioReference = %q, want empty for video recipe create", got)
	}
}

type staticReader struct {
	content string
}

// Open opens the requested resource.
func (r staticReader) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(r.content)), nil
}
