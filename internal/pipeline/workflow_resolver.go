package pipeline

import (
	"context"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
)

// WorkflowResolver resolves the orchestrator Workflows to use for a task.
// Deciding "can the shared Workflows be reused, or should task-specific Workflows be built for
// per-task options (overriding the model, seed or Veo settings)" and the building are gathered in
// the implementation (builder.workflowResolver); Runner only uses the result.
//
// There is no cleanup. It used to return a release that closed the resolved Workflows, but since
// go-veo-orchestrator passes reference images as gs:// as-is, there is no image-cache goroutine to
// stop, and Close itself disappeared from Workflows.
type WorkflowResolver interface {
	Resolve(context.Context, *domain.Task) (*orchestrator.Workflows, error)
}

// StaticWorkflowResolver is a test WorkflowResolver that always returns fixed Workflows.
type StaticWorkflowResolver struct {
	Workflows *orchestrator.Workflows
}

// Resolve returns the Workflows it holds as-is.
func (r StaticWorkflowResolver) Resolve(context.Context, *domain.Task) (*orchestrator.Workflows, error) {
	return r.Workflows, nil
}
