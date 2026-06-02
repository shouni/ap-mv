package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"ap-mv/internal/config"
	"ap-mv/internal/ports"
)

// MockVeoRunner は ports.VideoRunner の仮実装です。
// 実Veo APIは呼ばず、pipeline検証用の決定的なレスポンスだけを返します。
type MockVeoRunner struct {
	bucket string
}

func NewMockVeoRunner(cfg *config.Config) *MockVeoRunner {
	bucket := ""
	if cfg != nil {
		bucket = strings.TrimSpace(cfg.GCSBucket)
	}
	return &MockVeoRunner{bucket: bucket}
}

func (r *MockVeoRunner) Run(ctx context.Context, req ports.VideoGenerationRequest) (*ports.VideoResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if req.CutIndex < 0 {
		return nil, fmt.Errorf("cut_index must be non-negative")
	}
	if req.DurationSec <= 0 {
		return nil, fmt.Errorf("duration_sec must be positive")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	videoID := r.videoID(req)
	cloudURL := fmt.Sprintf("mock://veo/cuts/%03d.mp4", req.CutIndex)
	if r.bucket != "" {
		cloudURL = fmt.Sprintf("gs://%s/ap-mv/cuts/%03d.mp4", r.bucket, req.CutIndex)
	}
	return &ports.VideoResponse{
		CloudURL:    cloudURL,
		VideoID:     videoID,
		CutIndex:    req.CutIndex,
		DurationSec: req.DurationSec,
		MimeType:    "video/mp4",
	}, nil
}

func (r *MockVeoRunner) videoID(req ports.VideoGenerationRequest) string {
	src := strings.Join([]string{
		fmt.Sprintf("%d", req.CutIndex),
		req.Prompt,
		fmt.Sprintf("%.3f", req.DurationSec),
		fmt.Sprintf("%d", req.Seed),
		req.PreviousVideoID,
		req.ImageReference,
		req.AudioReference,
	}, "\x00")
	sum := sha256.Sum256([]byte(src))
	return "mock-veo-" + hex.EncodeToString(sum[:])[:24]
}
