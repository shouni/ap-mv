package ports

import "context"

// VideoGenerationRequest は Veo API に渡すマルチモーダルな入力をカプセル化します。
type VideoGenerationRequest struct {
	Prompt string
	// ImageReference があれば Veo adapter はこれを優先します。
	// 空の場合だけ InputImage をアップロードして参照 URI を作る想定です。
	ImageReference string
	AudioReference string
	InputImage     []byte
	InputAudio     []byte
	// PreviousVideoID は前カットの Video-to-Video 文脈を維持するための識別子です。
	PreviousVideoID string
	Seed            int64
	CutIndex        int
	DurationSec     float64
}

// VideoResponse は生成された動画のメタデータです。
type VideoResponse struct {
	CloudURL    string
	VideoID     string
	CutIndex    int
	DurationSec float64
	MimeType    string
	SizeBytes   int64
}

// VideoRunner は Veo API を叩いて1カットの動画を生成・管理する契約です。
type VideoRunner interface {
	Run(ctx context.Context, req VideoGenerationRequest) (*VideoResponse, error)
}

// AudioResolver は Music Recipe のカット列に音声セグメント参照を補完します。
type AudioResolver interface {
	Resolve(ctx context.Context, recipe *VideoRecipe) (*VideoRecipe, error)
}

// VideoTimelineRunner は Music Recipe のカット列を順次動画化する契約です。
type VideoTimelineRunner interface {
	Run(ctx context.Context, recipe *VideoRecipe) ([]*VideoResponse, error)
	RunAndSave(ctx context.Context, recipe *VideoRecipe, outputPath string) (*VideoPlotResponse, error)
}

// VideoPlotResponse は動画生成結果を反映したメタデータです。
type VideoPlotResponse struct {
	Recipe   *VideoRecipe
	Videos   []*VideoResponse
	Metadata *PublishResult
}
