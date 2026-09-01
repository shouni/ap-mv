package adapters

import (
	"context"
	"fmt"
	"strings"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/veo"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/ports"
)

// VertexVeoRunner は Vertex AI Veo の動画生成を呼び出す Runner です。
//
// API 呼び出しとポーリングは genai-kit の veo パッケージが持ち、この型は
// アプリ固有の関心だけを担当します: ジョブ単位の出力先の決定、生成物のジョブ配下
// 正規パスへのコピー、タスク単位のモデル・アスペクト比の差し替えです。
type VertexVeoRunner struct {
	videos           *veo.Client
	videoCopier      videoCopier
	model            string
	outputStorageURI string
	aspectRatio      string
	generateAudio    bool
	usePreviousVideo bool
}

// Close は参照を手放すだけです。
//
// 以前はここで専用の GCS クライアントを閉じていました。いまはストアを注入して
// もらう形なので、その寿命は注入した側（ファクトリ）にあります。
func (r *VertexVeoRunner) Close() error {
	if r == nil {
		return nil
	}
	r.videoCopier = nil
	return nil
}

// NewVertexVeoRunner はアプリケーション設定から VertexVeoRunner を生成します。
//
// aiClient は NewVertexAIAdapter が組み立てた Vertex AI クライアントを注入します。
// 自前で組まないのは、テキスト/画像生成と動画生成が別々のクライアントを持つと、
// リージョン・リトライ間隔・HTTP タイムアウトが片方だけ変更されて食い違うためです
// （実際に InitialDelay が 60s と 30s で割れていました）。認証とリトライの設定は
// これで1箇所に集約されます。
//
// store は、Veo の一時出力パスからジョブ配下の正規パスへ動画を移すために使います。
// 以前はここで GCS クライアントをもう 1 つ生成していましたが、
// remoteio.Store.Copy が同一スキームでサーバーサイドコピーへ落とすため不要になりました。
func NewVertexVeoRunner(cfg *config.Config, aiClient gemini.VideoGenerator, store remoteio.Store) (*VertexVeoRunner, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if aiClient == nil {
		return nil, fmt.Errorf("vertex AI client is required")
	}
	if cfg.Storage.GCSBucket == "" {
		return nil, fmt.Errorf("AP_MV_BUCKET is required")
	}

	videos, err := veo.New(aiClient,
		veo.WithPollInterval(cfg.AI.VeoPollInterval),
		veo.WithPollTimeout(cfg.AI.VeoOperationTimeout),
		veo.WithMaxPollErrors(cfg.AI.VeoPollMaxErrors),
	)
	if err != nil {
		return nil, fmt.Errorf("create Veo client: %w", err)
	}
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}

	return &VertexVeoRunner{
		videos:           videos,
		videoCopier:      store,
		model:            cfg.AI.VeoModel,
		outputStorageURI: buildVeoOutputStorageURI(cfg.Storage.GCSBucket, cfg.AI.VeoOutputPrefix),
		aspectRatio:      cfg.AI.VeoAspectRatio,
		generateAudio:    cfg.AI.VeoGenerateAudio,
		usePreviousVideo: cfg.AI.VeoUsePreviousVideo,
	}, nil
}

// WithVideoOptions は、モデルとアスペクト比だけを差し替えた派生 Runner を返します。
// veo クライアントやストアは共有するため、タスク単位で安全に呼び出せます。
// 空文字の指定は元の設定値を維持します。
func (r *VertexVeoRunner) WithVideoOptions(model, aspectRatio string) ports.VideoRunner {
	model = strings.TrimSpace(model)
	aspectRatio = strings.TrimSpace(aspectRatio)
	if (model == "" || model == r.model) && (aspectRatio == "" || aspectRatio == r.aspectRatio) {
		return r
	}
	derived := *r
	if model != "" {
		derived.model = model
	}
	if aspectRatio != "" {
		derived.aspectRatio = aspectRatio
	}
	return &derived
}

// Run は Veo の動画生成を実行し、完了後に生成動画のメタデータを返します。
func (r *VertexVeoRunner) Run(ctx context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	if err := validateVertexVeoRequest(req); err != nil {
		return nil, err
	}

	result, err := r.videos.Generate(ctx, r.model, r.buildRequest(ctx, req))
	if err != nil {
		return nil, fmt.Errorf("generate cut %d: %w", req.CutIndex, err)
	}
	video, ok := result.First()
	if !ok {
		return nil, fmt.Errorf("cut %d の生成結果に動画が含まれていません（operation: %s）", req.CutIndex, result.OperationName)
	}

	cloudURL, err := r.canonicalizeGeneratedVideo(ctx, req, video.URI)
	if err != nil {
		return nil, err
	}

	// VideoID は次カットの PreviousVideoURI として使うため、参照可能な gs:// URI
	// だけを載せる（VideoResponse.VideoID の契約）。以前は URI が無い場合に
	// オペレーション名へ退避していたが、その値は video_extension に分類されないのに
	// チェーン文脈として引き回され、7秒計画のカットが尺検証で誤って拒否されていた。
	videoID := cloudURL
	mimeType := video.MIMEType
	if mimeType == "" {
		mimeType = "video/mp4"
	}
	return &ports.VideoResponse{
		CloudURL:    cloudURL,
		VideoID:     videoID,
		CutIndex:    req.CutIndex,
		DurationSec: req.DurationSec,
		MimeType:    mimeType,
		SizeBytes:   int64(len(video.Data)),
	}, nil
}
