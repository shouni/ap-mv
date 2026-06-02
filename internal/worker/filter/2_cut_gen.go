package filter

import (
	"context"
	"fmt"
)

type CutKeyframeFilter struct{}

func (CutKeyframeFilter) Name() string { return "cut_keyframe_gen" }

func (CutKeyframeFilter) Execute(_ context.Context, fc *Context) error {
	if fc == nil || fc.Recipe == nil {
		return fmt.Errorf("cut keyframe generation requires recipe")
	}
	return fc.Recipe.Normalize()
}
