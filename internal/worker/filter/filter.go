package filter

import (
	"context"

	"ap-mv/internal/domain"
	"ap-mv/internal/ports"
)

type Context struct {
	Task        *domain.Task
	Recipe      *domain.MusicRecipe
	VideoRunner ports.VideoRunner
}

type Filter interface {
	Name() string
	Execute(ctx context.Context, fc *Context) error
}
