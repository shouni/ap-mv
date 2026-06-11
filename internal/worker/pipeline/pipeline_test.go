package pipeline

import (
	"context"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"ap-mv/internal/domain"
	"ap-mv/internal/worker/filter"
)

type noopFilter struct{}

// Name returns the receiver name.
func (noopFilter) Name() string { return "noop" }

// Execute runs the receiver processing step.
func (noopFilter) Execute(context.Context, *filter.Context) error { return nil }

// TestDefaultFiltersForComposeToKeyframeStopsAfterCutKeyframe verifies the keyframe-only default filter chain.
func TestDefaultFiltersForComposeToKeyframeStopsAfterCutKeyframe(t *testing.T) {
	filters := defaultFilters(domain.CommandComposeToKeyframe, nil)

	got := make([]string, 0, len(filters))
	for _, flt := range filters {
		got = append(got, flt.Name())
	}
	want := []string{"scripting", "cut_keyframe_gen"}
	if len(got) != len(want) {
		t.Fatalf("filters = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filters = %v, want %v", got, want)
		}
	}
}

// TestDefaultFiltersForComposeStillRunsFullPipeline verifies the full compose default filter chain.
func TestDefaultFiltersForComposeStillRunsFullPipeline(t *testing.T) {
	filters := defaultFilters(domain.CommandCompose, nil)

	got := make([]string, 0, len(filters))
	for _, flt := range filters {
		got = append(got, flt.Name())
	}
	want := []string{"scripting", "cut_keyframe_gen", "video_gen", "publishing"}
	if len(got) != len(want) {
		t.Fatalf("filters = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filters = %v, want %v", got, want)
		}
	}
}

// TestRunUsesWorkflowFactoryForSelectedModels verifies workflow creation for custom selected models.
func TestRunUsesWorkflowFactoryForSelectedModels(t *testing.T) {
	calls := 0
	runner := &Runner{
		OrchestratorConfig: orchestrator.Config{
			GeminiModel: "gemini-default",
			ImageModel:  "image-default",
		},
		Filters: []filter.Filter{noopFilter{}},
		WorkflowFactory: func(context.Context, *domain.Task) (*orchestrator.Workflows, error) {
			calls++
			return &orchestrator.Workflows{}, nil
		},
	}

	_, err := runner.Run(context.Background(), &domain.Task{
		JobID:    "job-1",
		Command:  domain.CommandCompose,
		Text:     "source",
		AIModels: domain.AIModels{TextModel: "gemini-alt", ImageModel: "image-default"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("workflow factory calls = %d, want 1", calls)
	}
}

// TestRunSkipsWorkflowFactoryForDefaultModels verifies default models reuse the runner workflows.
func TestRunSkipsWorkflowFactoryForDefaultModels(t *testing.T) {
	calls := 0
	runner := &Runner{
		OrchestratorConfig: orchestrator.Config{
			GeminiModel: "gemini-default",
			ImageModel:  "image-default",
		},
		Filters: []filter.Filter{noopFilter{}},
		WorkflowFactory: func(context.Context, *domain.Task) (*orchestrator.Workflows, error) {
			calls++
			return &orchestrator.Workflows{}, nil
		},
	}

	_, err := runner.Run(context.Background(), &domain.Task{
		JobID:    "job-1",
		Command:  domain.CommandCompose,
		Text:     "source",
		AIModels: domain.AIModels{TextModel: "gemini-default", ImageModel: "image-default"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("workflow factory calls = %d, want 0", calls)
	}
}
