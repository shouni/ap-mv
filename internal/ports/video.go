package ports

import (
	"context"
	"strings"

	veoports "github.com/shouni/go-veo-orchestrator/ports"
)

// VideoRunner は go-veo-orchestrator が定義する Veo adapter 境界です。
type VideoRunner = veoports.VideoRunner

// VideoGenerationRequest は go-veo-orchestrator の動画生成リクエスト型です。
type VideoGenerationRequest = veoports.VideoGenerationRequest

// VideoResponse は go-veo-orchestrator の動画生成レスポンス型です。
type VideoResponse = veoports.VideoResponse

// VideoRunnerConfigurator は、タスク単位で Veo モデルやアスペクト比を差し替えた
// 派生 Runner を返せる VideoRunner のオプションインターフェースです。
type VideoRunnerConfigurator interface {
	WithVideoOptions(model, aspectRatio string) VideoRunner
}

// ReferenceImagesSupporter は、reference_to_video（referenceImages、Veo 3系の非Fastモデルのみ
// 対応で8秒固定）を使えるかを問い合わせられる VideoRunner のオプションインターフェースです。
// カットの尺をVeoのサポート値へ正規化する処理が、モデル判定ロジックを重複実装せずに
// 「このカットは8秒固定になるか」を判断するために使います。
type ReferenceImagesSupporter interface {
	SupportsReferenceImages() bool
}

// DeriveVideoRunner は、runner が VideoRunnerConfigurator を実装していれば
// モデル・アスペクト比を差し替えた派生 Runner を返します。指定が両方空、
// または runner が差し替えに対応しない場合は runner をそのまま返します。
func DeriveVideoRunner(runner VideoRunner, model, aspectRatio string) VideoRunner {
	if runner == nil {
		return nil
	}
	model = strings.TrimSpace(model)
	aspectRatio = strings.TrimSpace(aspectRatio)
	if model == "" && aspectRatio == "" {
		return runner
	}
	configurator, ok := runner.(VideoRunnerConfigurator)
	if !ok {
		return runner
	}
	return configurator.WithVideoOptions(model, aspectRatio)
}

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
