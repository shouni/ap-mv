package event

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"ap-mv/internal/domain"
	"ap-mv/internal/worker/pipeline"
)

type Dispatcher struct {
	Pipeline *pipeline.Runner
}

func (d Dispatcher) Dispatch(ctx context.Context, body io.Reader) (*domain.MusicRecipe, error) {
	if d.Pipeline == nil {
		return nil, fmt.Errorf("pipeline is not configured")
	}
	var task domain.Task
	if err := json.NewDecoder(body).Decode(&task); err != nil {
		return nil, fmt.Errorf("decode task payload: %w", err)
	}
	return d.Pipeline.Run(ctx, &task)
}
