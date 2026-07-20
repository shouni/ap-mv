package adapters

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/ports"
)

const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"
const veoHTTPTimeout = 30 * time.Second

// VertexVeoRunner は Vertex AI Veo の長時間実行動画生成 API を呼び出す Runner です。
type VertexVeoRunner struct {
	client                   *http.Client
	videoCopier              videoCopier
	projectID                string
	locationID               string
	model                    string
	outputStorageURI         string
	aspectRatio              string
	generateAudio            bool
	pollInterval             time.Duration
	operationTimeout         time.Duration
	maxPollConsecutiveErrors int
	usePreviousVideo         bool
}

// Close は正規動画パスへのコピーに使う GCS クライアントを解放します。
func (r *VertexVeoRunner) Close() error {
	if r == nil || r.videoCopier == nil {
		return nil
	}
	copier := r.videoCopier
	r.videoCopier = nil
	if closer, ok := copier.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// NewVertexVeoRunner はアプリケーション設定から VertexVeoRunner を生成します。
//
// Vertex AI リクエスト用の Google ADC 認証と、Veo の一時出力パスから
// ジョブ配下の正規パスへ動画をコピーするための GCS クライアントを初期化します。
func NewVertexVeoRunner(ctx context.Context, cfg *config.Config) (*VertexVeoRunner, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if strings.TrimSpace(cfg.GCP.ProjectID) == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID is required")
	}
	locationID := strings.TrimSpace(cfg.AI.VeoLocationID)
	if locationID == "" {
		locationID = strings.TrimSpace(cfg.GCP.LocationID)
	}
	if locationID == "" {
		return nil, fmt.Errorf("VEO_LOCATION_ID or GCP_LOCATION_ID is required")
	}
	if strings.TrimSpace(cfg.Storage.GCSBucket) == "" {
		return nil, fmt.Errorf("AP_MV_BUCKET is required")
	}
	ts, err := google.DefaultTokenSource(ctx, cloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("create Google ADC token source: %w", err)
	}
	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create GCS client: %w", err)
	}

	pollInterval := cfg.AI.VeoPollInterval
	operationTimeout := cfg.AI.VeoOperationTimeout
	model := strings.TrimSpace(cfg.AI.VeoModel)
	aspectRatio := strings.TrimSpace(cfg.AI.VeoAspectRatio)

	baseClient := &http.Client{Timeout: veoHTTPTimeout}
	ctxWithClient := context.WithValue(ctx, oauth2.HTTPClient, baseClient)

	maxPollConsecutiveErrors := cfg.AI.VeoPollMaxErrors
	if maxPollConsecutiveErrors <= 0 {
		maxPollConsecutiveErrors = 10
	}
	return &VertexVeoRunner{
		client:                   oauth2.NewClient(ctxWithClient, ts),
		videoCopier:              &gcsVideoCopier{client: storageClient},
		projectID:                strings.TrimSpace(cfg.GCP.ProjectID),
		locationID:               locationID,
		model:                    model,
		outputStorageURI:         buildVeoOutputStorageURI(cfg.Storage.GCSBucket, cfg.AI.VeoOutputPrefix),
		aspectRatio:              aspectRatio,
		generateAudio:            cfg.AI.VeoGenerateAudio,
		pollInterval:             pollInterval,
		operationTimeout:         operationTimeout,
		maxPollConsecutiveErrors: maxPollConsecutiveErrors,
		usePreviousVideo:         cfg.AI.VeoUsePreviousVideo,
	}, nil
}

// WithVideoOptions は、モデルとアスペクト比だけを差し替えた派生 Runner を返します。
// HTTP クライアントや GCS クライアントは共有するため、タスク単位で安全に呼び出せます。
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

// Run は Veo の動画生成オペレーションを開始し、完了まで待って生成動画のメタデータを返します。
func (r *VertexVeoRunner) Run(ctx context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	if err := validateVertexVeoRequest(req); err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, r.operationTimeout)
	defer cancel()

	op, err := r.startOperation(runCtx, req)
	if err != nil {
		return nil, err
	}
	done, err := r.waitOperation(runCtx, op.Name)
	if err != nil {
		return nil, err
	}
	video, err := firstGeneratedVideo(done)
	if err != nil {
		return nil, err
	}
	video, err = r.canonicalizeGeneratedVideo(runCtx, req, video)
	if err != nil {
		return nil, err
	}
	videoID := video.GCSURI
	if videoID == "" {
		videoID = op.Name
	}
	mimeType := video.MimeType
	if mimeType == "" {
		mimeType = "video/mp4"
	}
	return &ports.VideoResponse{
		CloudURL:    video.GCSURI,
		VideoID:     videoID,
		CutIndex:    req.CutIndex,
		DurationSec: req.DurationSec,
		MimeType:    mimeType,
		SizeBytes:   video.SizeBytes,
	}, nil
}
