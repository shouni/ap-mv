package adapters

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/ap-mv/internal/ports"
)

// buildVeoOutputStorageURI は Vertex AI Veo が使う既定の GCS 出力ディレクトリを組み立てます。
func buildVeoOutputStorageURI(bucket, prefix string) string {
	cleanPrefix := strings.Trim(path.Clean("/"+strings.TrimSpace(prefix)), "/")
	return fmt.Sprintf("gs://%s/%s/", strings.TrimSpace(bucket), cleanPrefix)
}

// outputStorageURIFor はリクエストで使う Veo 出力ディレクトリを返します。
//
// context にジョブ単位の出力ベース URI がある場合は、後で安定したファイル名へ
// コピーできるように、Veo の出力先をカット単位の一時ディレクトリにします。
func (r *VertexVeoRunner) outputStorageURIFor(ctx context.Context, req ports.VideoGenerationRequest) string {
	if r == nil {
		return ""
	}
	if baseURI, ok := ports.VideoOutputBaseURIFromContext(ctx); ok {
		return buildVeoTemporaryCutOutputStorageURI(baseURI, req.CutIndex)
	}
	return r.outputStorageURI
}

// buildVeoTemporaryCutOutputStorageURI は生成カット単位の一時 GCS ディレクトリを組み立てます。
func buildVeoTemporaryCutOutputStorageURI(baseURI string, cutIndex int) string {
	baseURI = strings.TrimRight(strings.TrimSpace(baseURI), "/")
	if baseURI == "" {
		return ""
	}
	return fmt.Sprintf("%s/tmp/videos/cut_%02d/", baseURI, cutIndex)
}

// buildVeoCanonicalVideoURI は生成カット動画の安定した GCS URI を組み立てます。
func buildVeoCanonicalVideoURI(baseURI string, cutIndex int) string {
	baseURI = strings.TrimRight(strings.TrimSpace(baseURI), "/")
	if baseURI == "" {
		return ""
	}
	return fmt.Sprintf("%s/videos/cut_%02d.mp4", baseURI, cutIndex)
}

// canonicalizeGeneratedVideo は、ベース URI がある場合に生成動画をジョブ配下の安定した
// パスへコピーし、その URI を返します。Veo の出力先はカット単位の一時ディレクトリなので、
// メタデータへ残す URI と後段の結合処理が参照する URI を固定するための処理です。
// コピー対象が無い場合は sourceURI をそのまま返します。
func (r *VertexVeoRunner) canonicalizeGeneratedVideo(ctx context.Context, req ports.VideoGenerationRequest, sourceURI string) (string, error) {
	sourceURI = strings.TrimSpace(sourceURI)
	if r == nil || r.videoCopier == nil || sourceURI == "" {
		return sourceURI, nil
	}
	baseURI, ok := ports.VideoOutputBaseURIFromContext(ctx)
	if !ok {
		return sourceURI, nil
	}
	targetURI := buildVeoCanonicalVideoURI(baseURI, req.CutIndex)
	if targetURI == "" || targetURI == sourceURI {
		return sourceURI, nil
	}
	if err := r.videoCopier.Copy(ctx, sourceURI, targetURI); err != nil {
		return "", fmt.Errorf("copy generated video to canonical path: %w", err)
	}
	if err := r.videoCopier.Delete(ctx, sourceURI); err != nil {
		slog.WarnContext(ctx, "failed to delete temporary Veo video after canonical copy",
			"source_uri", sourceURI,
			"target_uri", targetURI,
			"error", err,
		)
	}
	return targetURI, nil
}

// videoCopier は、生成物を正規パスへ移すために必要な操作だけを表します。
// remoteio.Store がそのまま満たします。
//
// 以前はこの口のために GCS クライアントをもう 1 つ持ち、CopierFrom を自前で
// 呼んでいました。remoteio.Store.Copy が同一スキームならサーバーサイドコピーへ
// 落とすようになったので、注入済みのストアで足ります。
type videoCopier interface {
	Copy(ctx context.Context, src, dst string, opts ...remoteio.WriteOption) error
	Delete(ctx context.Context, uri string) error
}
