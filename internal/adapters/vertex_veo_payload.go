package adapters

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/shouni/ap-mv/internal/ports"
)

// buildGenerateBody は内部の動画生成リクエストを Vertex AI Veo のリクエスト本文へ変換します。
func (r *VertexVeoRunner) buildGenerateBody(ctx context.Context, req ports.VideoGenerationRequest) map[string]any {
	instance := map[string]any{
		"prompt": strings.TrimSpace(req.Prompt),
	}
	hasVideoContext := false
	if r.usePreviousVideo {
		if media := previousVideoMedia(req.PreviousVideoID); media != nil {
			instance["video"] = media
			hasVideoContext = true
		}
	}
	// Veo は video / referenceImages / image を同一リクエストで併用できないため、
	// video-to-video 文脈がある場合は画像参照を送らない。referenceImages は
	// 対応モデル（Veo 3 系、Fast を除く）のみで使い、非対応モデルはキーフレームの
	// image 入力（image-to-video）へフォールバックする。
	if !hasVideoContext {
		if refs := referenceImagesMedia(req); refs != nil && r.modelSupportsReferenceImages() {
			instance["referenceImages"] = refs
		} else if media := imageMedia(req); media != nil {
			instance["image"] = media
		}
	}
	if media := audioMedia(req); media != nil {
		instance["audio"] = media
	}

	parameters := map[string]any{
		"storageUri":      r.outputStorageURIFor(ctx, req),
		"sampleCount":     1,
		"durationSeconds": int(math.Round(req.DurationSec)),
		"generateAudio":   r.generateAudio,
	}
	if r.aspectRatio != "" {
		parameters["aspectRatio"] = r.aspectRatio
	}
	if req.Seed != 0 {
		parameters["seed"] = req.Seed
	}

	return map[string]any{
		"instances":  []any{instance},
		"parameters": parameters,
	}
}

// modelSupportsReferenceImages は使用モデルが referenceImages（asset 参照）に対応するかを返します。
// referenceImages は Veo 3 系のみサポートで、Veo 3.1 Fast は非対応です。
func (r *VertexVeoRunner) modelSupportsReferenceImages() bool {
	model := strings.ToLower(r.model)
	return strings.HasPrefix(model, "veo-3") && !strings.Contains(model, "fast")
}

// SupportsReferenceImages は modelSupportsReferenceImages を公開し、ports.ReferenceImagesSupporter を
// 満たします。呼び出し元（カットの尺をVeoのサポート値へ正規化する処理）が、このRunnerが
// reference_to_video（referenceImages、8秒固定）を使うか image_to_video（{4,6,8}秒）を使うかを、
// モデル判定ロジックを重複実装せずに問い合わせるための入り口です。
func (r *VertexVeoRunner) SupportsReferenceImages() bool {
	return r.modelSupportsReferenceImages()
}

// validateVertexVeoRequest は Veo API アダプターに必要なリクエスト項目を検証します。
func validateVertexVeoRequest(req ports.VideoGenerationRequest) error {
	if strings.TrimSpace(req.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if req.CutIndex < 0 {
		return fmt.Errorf("cut_index must be non-negative")
	}
	if req.DurationSec <= 0 {
		return fmt.Errorf("duration_sec must be positive")
	}
	if req.Seed < 0 || req.Seed > math.MaxUint32 {
		return fmt.Errorf("seed must be between 0 and %d", math.MaxUint32)
	}
	return nil
}

// referenceImagesMedia は ReferenceImages から Veo の referenceImages payload を組み立てます。
// URI が空のものは除外し、結果が0件の場合は nil を返します。
func referenceImagesMedia(req ports.VideoGenerationRequest) []map[string]any {
	if len(req.ReferenceImages) == 0 {
		return nil
	}
	var result []map[string]any
	for _, uri := range req.ReferenceImages {
		if uri = strings.TrimSpace(uri); uri == "" {
			continue
		}
		result = append(result, map[string]any{
			"image": map[string]any{
				"gcsUri":   uri,
				"mimeType": mimeTypeFromURI(uri, "image/png"),
			},
			"referenceType": "asset",
		})
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// imageMedia は GCS 画像参照またはインライン画像バイト列から Veo の画像入力 payload を組み立てます。
func imageMedia(req ports.VideoGenerationRequest) map[string]any {
	if ref := strings.TrimSpace(req.ImageReference); ref != "" {
		return map[string]any{
			"gcsUri":   ref,
			"mimeType": mimeTypeFromURI(ref, "image/png"),
		}
	}
	if len(req.InputImage) == 0 {
		return nil
	}
	return map[string]any{
		"bytesBase64Encoded": base64.StdEncoding.EncodeToString(req.InputImage),
		"mimeType":           detectedImageMimeType(req.InputImage),
	}
}

// previousVideoMedia は前回生成動画の GCS URI から Veo の動画継続 payload を組み立てます。
func previousVideoMedia(previousVideoID string) map[string]any {
	ref := strings.TrimSpace(previousVideoID)
	if !strings.HasPrefix(ref, "gs://") {
		return nil
	}
	return map[string]any{
		"gcsUri":   ref,
		"mimeType": mimeTypeFromURI(ref, "video/mp4"),
	}
}

// audioMedia は GCS 音声参照またはインライン音声バイト列から Veo の音声入力 payload を組み立てます。
func audioMedia(req ports.VideoGenerationRequest) map[string]any {
	if ref := strings.TrimSpace(req.AudioReference); ref != "" {
		return map[string]any{
			"gcsUri":   ref,
			"mimeType": mimeTypeFromURI(ref, "audio/mpeg"),
		}
	}
	if len(req.InputAudio) == 0 {
		return nil
	}
	return map[string]any{
		"bytesBase64Encoded": base64.StdEncoding.EncodeToString(req.InputAudio),
		"mimeType":           detectedAudioMimeType(req.InputAudio),
	}
}

// detectedAudioMimeType は対応するインライン音声 MIME type を検出し、不明な場合は audio/mpeg を返します。
func detectedAudioMimeType(data []byte) string {
	if len(data) == 0 {
		return "audio/mpeg"
	}
	limit := 512
	if len(data) < limit {
		limit = len(data)
	}
	mimeType := http.DetectContentType(data[:limit])
	switch mimeType {
	case "audio/mpeg", "audio/wav", "audio/ogg", "audio/flac", "audio/mp4":
		return mimeType
	default:
		return "audio/mpeg"
	}
}

// detectedImageMimeType は対応するインライン画像 MIME type を検出し、不明な場合は image/png を返します。
func detectedImageMimeType(data []byte) string {
	mimeType := http.DetectContentType(data)
	switch mimeType {
	case "image/jpeg", "image/png", "image/webp":
		return mimeType
	default:
		return "image/png"
	}
}

// mimeTypeFromURI は URI の拡張子から media MIME type を推定し、不明な場合は fallback を返します。
func mimeTypeFromURI(uri, fallback string) string {
	lower := strings.ToLower(uri)
	switch {
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".mov"):
		return "video/mov"
	case strings.HasSuffix(lower, ".mpeg"):
		return "video/mpeg"
	case strings.HasSuffix(lower, ".mp4"):
		return "video/mp4"
	case strings.HasSuffix(lower, ".mpg"):
		return "video/mpg"
	case strings.HasSuffix(lower, ".avi"):
		return "video/avi"
	case strings.HasSuffix(lower, ".wmv"):
		return "video/wmv"
	case strings.HasSuffix(lower, ".flv"):
		return "video/flv"
	default:
		return fallback
	}
}
