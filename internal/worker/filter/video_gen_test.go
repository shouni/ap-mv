package filter

import (
	"context"
	"errors"
	"testing"

	"ap-mv/internal/domain"
	"ap-mv/internal/ports"
)

func TestVideoGenerationFilterEnqueuesContinuationAfterOneCut(t *testing.T) {
	recipe := &domain.MusicRecipe{
		Title: "test",
		Cuts: []domain.VideoCut{
			{Index: 0, DurationSec: 8, Prompt: "first"},
			{Index: 1, DurationSec: 8, Prompt: "second"},
		},
	}
	task := &domain.Task{
		JobID:   "job-1",
		Command: domain.CommandGenerateFromRecipe,
		Recipe:  recipe,
	}
	queue := &captureQueue{}
	flt := VideoGenerationFilter{Runner: sequenceRunner{}}

	err := flt.Execute(context.Background(), &Context{
		Task:      task,
		Recipe:    recipe,
		TaskQueue: queue,
	})
	if !errors.Is(err, ErrPipelineDeferred) {
		t.Fatalf("Execute() error = %v, want ErrPipelineDeferred", err)
	}
	if recipe.Cuts[0].Status != domain.CutStatusGenerated {
		t.Fatalf("first cut status = %q, want %q", recipe.Cuts[0].Status, domain.CutStatusGenerated)
	}
	if recipe.Cuts[1].Status == domain.CutStatusGenerated {
		t.Fatalf("second cut should not be generated in the same invocation")
	}
	if len(queue.tasks) != 1 {
		t.Fatalf("enqueued tasks = %d, want 1", len(queue.tasks))
	}
	if queue.tasks[0].Command != domain.CommandGenerateFromRecipe {
		t.Fatalf("continuation command = %q, want %q", queue.tasks[0].Command, domain.CommandGenerateFromRecipe)
	}
	if queue.tasks[0].Recipe.Cuts[0].VideoID == "" {
		t.Fatalf("continuation task did not include generated cut state")
	}
}

type sequenceRunner struct{}

func (sequenceRunner) Run(_ context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	return &ports.VideoResponse{
		CloudURL: "gs://bucket/cut.mp4",
		VideoID:  "video-id",
		CutIndex: req.CutIndex,
	}, nil
}

type captureQueue struct {
	tasks []*domain.Task
}

func (q *captureQueue) Enqueue(_ context.Context, task *domain.Task) error {
	copied := *task
	q.tasks = append(q.tasks, &copied)
	return nil
}
