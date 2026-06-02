package filter

import (
	"context"
	"fmt"
)

type PublishingFilter struct{}

func (PublishingFilter) Name() string { return "publishing" }

func (PublishingFilter) Execute(_ context.Context, fc *Context) error {
	if fc == nil || fc.Recipe == nil {
		return fmt.Errorf("publishing requires recipe")
	}
	return fc.Recipe.Normalize()
}
