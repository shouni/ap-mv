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
// どの生成機能（video / referenceImages / image+lastFrame / image）でリクエストを組むかは
// ports.ClassifyVeoRequest に一本化されており、filter 側のプロンプト・尺選択と構造的に
// 一致します（Veo は video / referenceImages / image を同一リクエストで併用できません）。
func (r *VertexVeoRunner) buildGenerateBody(ctx context.Context, req ports.VideoGenerationRequest) map[string]any {
	instance := map[string]any{
		"prompt": strings.TrimSpace(req.Prompt),
	}
	switch ports.ClassifyVeoRequest(req, r.usePreviousVideo, r.capabilities()) {
	case ports.VeoModeVideoExtension:
		instance["video"] = previousVideoMedia(req.PreviousVideoID)
	case ports.VeoModeReferenceToVideo:
		instance["referenceImages"] = referenceImagesMedia(req)
	case ports.VeoModeFramesToVideo:
		instance["image"] = imageMedia(req)
		instance["lastFrame"] = lastFrameMedia(req)
	case ports.VeoModeImageToVideo:
		// 画像参照が一切ないリクエスト（テキストのみ）も image_to_video に分類される
		// ため、image は存在するときだけ送る。
		if media := imageMedia(req); media != nil {
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

// capabilities は使用モデルの Veo オプション機能対応状況を ports.VeoCapabilities として
// 返します（ports.ClassifyVeoRequest の入力）。
func (r *VertexVeoRunner) capabilities() ports.VeoCapabilities {
	return ports.VeoCapabilities{
		ReferenceImages: r.modelSupportsReferenceImages(),
		LastFrame:       r.modelSupportsLastFrame(),
	}
}

// modelSupportsReferenceImages は使用モデルが referenceImages（asset 参照）に対応するかを返します。
// referenceImages は Veo 3 系のみサポートで、Veo 3.1 Fast は非対応です。
func (r *VertexVeoRunner) modelSupportsReferenceImages() bool {
	model := strings.ToLower(r.model)
	return strings.HasPrefix(model, "veo-3") && !strings.Contains(model, "fast")
}

// modelSupportsLastFrame は使用モデルが lastFrame（first/last frame 補間）に対応するかを
// 返します。Vertex AI で対応するのは veo-2.0-generate-001 / veo-3.1-generate-001 /
// veo-3.1-fast-generate-001 で、Veo 3.0 系は非対応です（referenceImages と違い Fast も対応）。
func (r *VertexVeoRunner) modelSupportsLastFrame() bool {
	model := strings.ToLower(r.model)
	return strings.HasPrefix(model, "veo-2") || strings.HasPrefix(model, "veo-3.1")
}

// SupportsLastFrame は modelSupportsLastFrame を公開し、ports.LastFrameSupporter を満たします。
// video_gen フィルタが、次カットのキーフレームを lastFrame（frames_to_video 補間）として
// 渡すかを、モデル判定ロジックを重複実装せずに問い合わせるための入り口です。
func (r *VertexVeoRunner) SupportsLastFrame() bool {
	return r.modelSupportsLastFrame()
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

// lastFrameMedia は LastFrameReference から Veo の lastFrame（終了フレーム）payload を組み立てます。
func lastFrameMedia(req ports.VideoGenerationRequest) map[string]any {
	ref := strings.TrimSpace(req.LastFrameReference)
	if ref == "" {
		return nil
	}
	return map[string]any{
		"gcsUri":   ref,
		"mimeType": mimeTypeFromURI(ref, "image/png"),
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
