package ports

import (
	"context"
	"strings"
)

import veoports "github.com/shouni/go-veo-orchestrator/ports"

// VideoRunner は go-veo-orchestrator が定義する Veo adapter 境界です。
type VideoRunner = veoports.VideoRunner

// VideoGenerationRequest は go-veo-orchestrator の動画生成リクエスト型です。
type VideoGenerationRequest = veoports.VideoGenerationRequest

// VideoResponse は go-veo-orchestrator の動画生成レスポンス型です。
type VideoResponse = veoports.VideoResponse

type videoOutputBaseURIKey struct{}

// WithVideoOutputBaseURI stores the job-scoped output base URI for video runners.
func WithVideoOutputBaseURI(ctx context.Context, baseURI string) context.Context {
	baseURI = strings.TrimSpace(baseURI)
	if baseURI == "" {
		return ctx
	}
	return context.WithValue(ctx, videoOutputBaseURIKey{}, baseURI)
}

// VideoOutputBaseURIFromContext returns the job-scoped output base URI, if set.
func VideoOutputBaseURIFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	baseURI, ok := ctx.Value(videoOutputBaseURIKey{}).(string)
	baseURI = strings.TrimSpace(baseURI)
	return baseURI, ok && baseURI != ""
}
