package filter

import (
	"context"
	"errors"

	"ap-mv/internal/domain"
	"ap-mv/internal/ports"
)

var ErrPipelineDeferred = errors.New("pipeline deferred")

type Context struct {
	Task        *domain.Task
	Recipe      *domain.MusicRecipe
	VideoRunner ports.VideoRunner
	TaskQueue   ports.TaskQueue
}

type Filter interface {
	Name() string
	Execute(ctx context.Context, fc *Context) error
}
