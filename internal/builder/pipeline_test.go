package builder

import (
	"context"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
)

// TestNewWorkflowResolverPassesOrchestratorConfig verifies that resolver construction forwards
// orchestrator config into the workflow resolver's decision logic.
func TestNewWorkflowResolverPassesOrchestratorConfig(t *testing.T) {
	cfg := &config.Config{
		AI: config.AIConfig{
			GeminiModel: "gemini-text",
			ImageModel:  "gemini-image",
		},
	}

	resolver := newWorkflowResolver(cfg, nil, nil, nil, &orchestrator.Workflows{})

	if resolver.orchCfg.GeminiModel != "gemini-text" {
		t.Fatalf("GeminiModel = %q", resolver.orchCfg.GeminiModel)
	}
	if resolver.orchCfg.ImageModel != "gemini-image" {
		t.Fatalf("ImageModel = %q", resolver.orchCfg.ImageModel)
	}
}

// TestWorkflowResolverBuildsForSelectedModels verifies that a task selecting non-default
// models triggers a task-specific workflow build instead of reusing the shared workflows.
func TestWorkflowResolverBuildsForSelectedModels(t *testing.T) {
	calls := 0
	built := &orchestrator.Workflows{}
	resolver := &workflowResolver{
		orchCfg: orchestrator.Config{GeminiModel: "gemini-default", ImageModel: "image-default"},
		shared:  &orchestrator.Workflows{},
		build: func(context.Context, *domain.Task) (*orchestrator.Workflows, error) {
			calls++
			return built, nil
		},
	}

	got, err := resolver.Resolve(t.Context(), &domain.Task{
		AIModels: domain.AIModels{TextModel: "gemini-alt", ImageModel: "image-default"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("build calls = %d, want 1", calls)
	}
	if got != built {
		t.Fatal("Resolve() did not return the task-specific workflows")
	}
}

// TestWorkflowResolverReusesSharedForDefaultOptions verifies that default models without
// seed or Veo overrides reuse the shared workflows.
func TestWorkflowResolverReusesSharedForDefaultOptions(t *testing.T) {
	calls := 0
	shared := &orchestrator.Workflows{}
	resolver := &workflowResolver{
		orchCfg: orchestrator.Config{GeminiModel: "gemini-default", ImageModel: "image-default"},
		shared:  shared,
		build: func(context.Context, *domain.Task) (*orchestrator.Workflows, error) {
			calls++
			return &orchestrator.Workflows{}, nil
		},
	}

	got, err := resolver.Resolve(t.Context(), &domain.Task{
		AIModels: domain.AIModels{TextModel: "gemini-default", ImageModel: "image-default"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("build calls = %d, want 0", calls)
	}
	if got != shared {
		t.Fatal("Resolve() did not return the shared workflows")
	}
}

// TestWorkflowResolverBuildsForTaskOverrides verifies that seed and Veo option overrides
// each force a task-specific workflow build even with default models.
func TestWorkflowResolverBuildsForTaskOverrides(t *testing.T) {
	seed := int64(42)
	tests := []struct {
		name string
		task *domain.Task
	}{
		{
			name: "seed override",
			task: &domain.Task{SeedOverride: &seed, SeedOverrideCharacterID: "char-1"},
		},
		{
			name: "veo model override",
			task: &domain.Task{VeoModel: "veo-alt"},
		},
		{
			name: "veo aspect ratio override",
			task: &domain.Task{VeoAspectRatio: "9:16"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			resolver := &workflowResolver{
				shared: &orchestrator.Workflows{},
				build: func(context.Context, *domain.Task) (*orchestrator.Workflows, error) {
					calls++
					return &orchestrator.Workflows{}, nil
				},
			}
			if _, err := resolver.Resolve(t.Context(), tt.task); err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if calls != 1 {
				t.Fatalf("build calls = %d, want 1", calls)
			}
		})
	}
}
