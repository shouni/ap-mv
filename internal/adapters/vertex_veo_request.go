package adapters

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-gemini-client/veo"

	"github.com/shouni/ap-mv/internal/ports"
)

// buildRequest は内部の動画生成リクエストを veo パッケージのリクエストへ変換します。
// どの生成機能（video / referenceImages / image+lastFrame / image）で組むかは
// ports.ClassifyVeoRequest に一本化されており、filter 側のプロンプト・尺選択と
// 構造的に一致します（Veo は video / referenceImages / image を同一リクエストで
// 併用できません）。
func (r *VertexVeoRunner) buildRequest(ctx context.Context, req ports.VideoGenerationRequest) veo.Request {
	out := veo.Request{
		Prompt:        strings.TrimSpace(req.Prompt),
		DurationSec:   int(math.Round(req.DurationSec)),
		AspectRatio:   r.aspectRatio,
		GenerateAudio: &r.generateAudio,
		OutputGCSURI:  r.outputStorageURIFor(ctx, req),
	}
	if req.Seed != 0 {
		seed := req.Seed
		out.Seed = &seed
	}

	switch ports.ClassifyVeoRequest(req, r.usePreviousVideo, r.capabilities()) {
	case ports.VeoModeVideoExtension:
		out.Video = previousVideoMedia(req.PreviousVideoURI)
	case ports.VeoModeReferenceToVideo:
		out.References = referenceImagesMedia(req)
	case ports.VeoModeFramesToVideo:
		out.Image = imageMedia(req)
		out.LastFrame = lastFrameMedia(req)
	case ports.VeoModeImageToVideo:
		// 画像参照が一切ないリクエスト（テキストのみ）も image_to_video に分類される
		// ため、image は存在するときだけ設定する。
		out.Image = imageMedia(req)
	}

	if audio := audioMedia(req); audio != nil {
		out.ModifyRequestBody = injectAudioInstance(*audio)
	}
	return out
}

// injectAudioInstance は、組み立て済みリクエストボディの instances[0] へ音声入力を
// 差し込むフックを返します。
//
// SDK は動画生成の音声入力を型として持っていないため、ボディを直接書き換えます。
// ExtraBody ではこの位置へ届きません（マージがマップ同士でしか再帰せず、instances は
// 配列なので丸ごと置き換わり、prompt や画像入力が消えます）。
//
// なお、この audio 入力を Veo が実際に解釈しているかは未検証です。公式の SDK にも
// 対応するフィールドが無いため、無視されている可能性があります。1カットで有無を
// 比較して確かめた上で、効果が無ければこの経路ごと削除してください。
func injectAudioInstance(audio map[string]any) func(map[string]any) map[string]any {
	return func(body map[string]any) map[string]any {
		instances, ok := body["instances"].([]any)
		if !ok || len(instances) == 0 {
			return body
		}
		instance, ok := instances[0].(map[string]any)
		if !ok {
			return body
		}
		instance["audio"] = audio
		return body
	}
}

// capabilities は使用モデルの Veo オプション機能対応状況を ports.VeoCapabilities として
// 返します（ports.ClassifyVeoRequest の入力）。モデル名→対応機能の規則は
// ports.VeoModelCapabilities が唯一の定義元です（以前はここで文字列前方一致を
// 再導出しており、「ルールはライブラリが持つ」という原則が破れていました）。
func (r *VertexVeoRunner) capabilities() ports.VeoCapabilities {
	return ports.VeoModelCapabilities(r.model)
}

// SupportsLastFrame は ports.LastFrameSupporter を満たします。
// video_gen フィルタが、次カットのキーフレームを lastFrame（frames_to_video 補間）として
// 渡すかを、モデル判定ロジックを重複実装せずに問い合わせるための入り口です。
func (r *VertexVeoRunner) SupportsLastFrame() bool {
	return r.capabilities().LastFrame
}

// SupportsReferenceImages は ports.ReferenceImagesSupporter を満たします。呼び出し元
// （カットの尺をVeoのサポート値へ正規化する処理）が、このRunnerが reference_to_video
// （referenceImages、8秒固定）を使うか image_to_video（{4,6,8}秒）を使うかを、
// モデル判定ロジックを重複実装せずに問い合わせるための入り口です。
func (r *VertexVeoRunner) SupportsReferenceImages() bool {
	return r.capabilities().ReferenceImages
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
	if req.Seed < 0 || req.Seed > math.MaxInt32 {
		return fmt.Errorf("seed must be between 0 and %d", math.MaxInt32)
	}
	return nil
}

// referenceImagesMedia は ReferenceImages から Veo の参照画像リストを組み立てます。
// URI が空のものは除外し、結果が0件の場合は nil を返します。
func referenceImagesMedia(req ports.VideoGenerationRequest) []veo.Reference {
	var result []veo.Reference
	for _, uri := range req.ReferenceImages {
		if uri = strings.TrimSpace(uri); uri == "" {
			continue
		}
		result = append(result, veo.Reference{
			Image: veo.Media{URI: uri, MIMEType: mimeTypeFromURI(uri, "image/png")},
			Type:  gemini.VideoReferenceAsset,
		})
	}
	return result
}

// imageMedia は GCS 画像参照またはインライン画像バイト列から Veo の画像入力を組み立てます。
func imageMedia(req ports.VideoGenerationRequest) *veo.Media {
	if ref := strings.TrimSpace(req.ImageReference); ref != "" {
		return &veo.Media{URI: ref, MIMEType: mimeTypeFromURI(ref, "image/png")}
	}
	if len(req.InputImage) == 0 {
		return nil
	}
	return &veo.Media{Data: req.InputImage, MIMEType: detectedImageMimeType(req.InputImage)}
}

// lastFrameMedia は LastFrameReference から Veo の終了フレーム入力を組み立てます。
func lastFrameMedia(req ports.VideoGenerationRequest) *veo.Media {
	ref := strings.TrimSpace(req.LastFrameReference)
	if ref == "" {
		return nil
	}
	return &veo.Media{URI: ref, MIMEType: mimeTypeFromURI(ref, "image/png")}
}

// previousVideoMedia は前回生成動画の GCS URI から Veo の動画継続入力を組み立てます。
func previousVideoMedia(previousVideoID string) *veo.Media {
	ref := strings.TrimSpace(previousVideoID)
	if !strings.HasPrefix(ref, "gs://") {
		return nil
	}
	return &veo.Media{URI: ref, MIMEType: mimeTypeFromURI(ref, "video/mp4")}
}

// audioMedia は GCS 音声参照またはインライン音声バイト列から Veo の音声入力 payload を
// 組み立てます。SDK が型を持たないため、生のマップのまま扱います。
func audioMedia(req ports.VideoGenerationRequest) *map[string]any {
	if ref := strings.TrimSpace(req.AudioReference); ref != "" {
		return &map[string]any{
			"gcsUri":   ref,
			"mimeType": mimeTypeFromURI(ref, "audio/mpeg"),
		}
	}
	if len(req.InputAudio) == 0 {
		return nil
	}
	return &map[string]any{
		"bytesBase64Encoded": req.InputAudio,
		"mimeType":           detectedAudioMimeType(req.InputAudio),
	}
}

// detectedAudioMimeType は対応するインライン音声 MIME type を検出し、不明な場合は audio/mpeg を返します。
func detectedAudioMimeType(data []byte) string {
	if len(data) == 0 {
		return "audio/mpeg"
	}
	limit := min(len(data), 512)
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
