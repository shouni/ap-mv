package domain

import (
	"strings"
	"testing"
)

func TestTaskValidateRequiresGCSURIForRecipeAndAudio(t *testing.T) {
	tests := []struct {
		name string
		task Task
		want string
	}{
		{
			name: "generate_from_recipe rejects http recipe url",
			task: Task{
				JobID:     "job-1",
				Command:   CommandGenerateFromRecipe,
				RecipeURL: "https://example.com/recipe.json",
			},
			want: "recipe_url must be a valid GCS URI",
		},
		{
			name: "generate_from_recipe rejects local audio url",
			task: Task{
				JobID:     "job-1",
				Command:   CommandGenerateFromRecipe,
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

func TestTaskValidateAcceptsGCSRecipeAndAudioURL(t *testing.T) {
	task := Task{
		JobID:     "job-1",
		Command:   CommandGenerateFromRecipe,
		RecipeURL: "gs://bucket/recipe.json",
		AudioURL:  "gs://bucket/music.mp3",
	}

	if err := task.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestTaskValidateAcceptsComposeToKeyframe(t *testing.T) {
	task := Task{
		JobID:   "job-1",
		Command: CommandComposeToKeyframe,
		Text:    "source",
	}

	if err := task.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
