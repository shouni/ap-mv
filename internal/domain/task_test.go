package domain

import (
	"strings"
	"testing"
)

// TestTaskValidateRequiresGCSURIForRecipeAndAudio verifies that recipe and audio URLs must be GCS URIs.
func TestTaskValidateRequiresGCSURIForRecipeAndAudio(t *testing.T) {
	tests := []struct {
		name string
		task Task
		want string
	}{
		{
			name: "mv_from_keyframe_video_recipe rejects http recipe url",
			task: Task{
				JobID:     "job-1",
				Command:   CommandMVFromKeyframeVideoRecipe,
				RecipeURL: "https://example.com/recipe.json",
			},
			want: "recipe_url must be a valid GCS URI",
		},
		{
			name: "mv_from_keyframe_video_recipe rejects local audio url",
			task: Task{
				JobID:     "job-1",
				Command:   CommandMVFromKeyframeVideoRecipe,
				RecipeURL: "gs://bucket/recipe.json",
				AudioURL:  "/tmp/music.mp3",
			},
			want: "audio_url must be a valid GCS URI",
		},
		{
			name: "compose rejects http audio url",
			task: Task{
				JobID:    "job-1",
				Command:  CommandCompose,
				Text:     "source",
				AudioURL: "https://example.com/music.mp3",
			},
			want: "audio_url must be a valid GCS URI",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.task.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

// TestTaskValidateAcceptsGCSRecipeAndAudioURL verifies that GCS recipe and audio URLs are accepted.
func TestTaskValidateAcceptsGCSRecipeAndAudioURL(t *testing.T) {
	task := Task{
		JobID:     "job-1",
		Command:   CommandMVFromKeyframeVideoRecipe,
		RecipeURL: "gs://bucket/recipe.json",
		AudioURL:  "gs://bucket/music.mp3",
	}

	if err := task.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// TestTaskValidateShortVideoFromSection verifies short_video_from_section validation rules.
func TestTaskValidateShortVideoFromSection(t *testing.T) {
	sectionIndex := 1
	negativeIndex := -1

	valid := Task{
		JobID:          "job-1",
		Command:        CommandShortVideoFromSection,
		RecipeURL:      "gs://bucket/jobs/job-1/video_music_meta.json",
		SectionIndex:   &sectionIndex,
		VeoAspectRatio: "9:16",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name string
		task Task
		want string
	}{
		{
			name: "requires section_index",
			task: Task{
				JobID:     "job-1",
				Command:   CommandShortVideoFromSection,
				RecipeURL: "gs://bucket/recipe.json",
			},
			want: "requires a non-negative section_index",
		},
		{
			name: "rejects negative section_index",
			task: Task{
				JobID:        "job-1",
				Command:      CommandShortVideoFromSection,
				RecipeURL:    "gs://bucket/recipe.json",
				SectionIndex: &negativeIndex,
			},
			want: "requires a non-negative section_index",
		},
		{
			name: "requires recipe input",
			task: Task{
				JobID:        "job-1",
				Command:      CommandShortVideoFromSection,
				SectionIndex: &sectionIndex,
			},
			want: "requires recipe, video_recipe, or recipe_url",
		},
		{
			name: "rejects http recipe url",
			task: Task{
				JobID:        "job-1",
				Command:      CommandShortVideoFromSection,
				RecipeURL:    "https://example.com/recipe.json",
				SectionIndex: &sectionIndex,
			},
			want: "recipe_url must be a valid GCS URI",
		},
		{
			name: "rejects unsupported aspect ratio",
			task: Task{
				JobID:          "job-1",
				Command:        CommandShortVideoFromSection,
				RecipeURL:      "gs://bucket/recipe.json",
				SectionIndex:   &sectionIndex,
				VeoAspectRatio: "4:3",
			},
			want: "veo_aspect_ratio must be 16:9 or 9:16",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.task.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

// TestTaskValidateAcceptsVideoRecipeCreate verifies that video recipe creation tasks validate successfully.
func TestTaskValidateAcceptsVideoRecipeCreate(t *testing.T) {
	task := Task{
		JobID:   "job-1",
		Command: CommandVideoRecipeCreate,
		Text:    "source",
	}

	if err := task.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
