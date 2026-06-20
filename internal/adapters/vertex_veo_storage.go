package adapters

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/shouni/go-remote-io/remoteio"

	"ap-mv/internal/ports"
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

// canonicalizeGeneratedVideo はベース URI がある場合、生成動画をジョブ配下の安定したパスへコピーします。
func (r *VertexVeoRunner) canonicalizeGeneratedVideo(ctx context.Context, req ports.VideoGenerationRequest, video vertexVideo) (vertexVideo, error) {
	if r == nil || r.videoCopier == nil || strings.TrimSpace(video.GCSURI) == "" {
		return video, nil
	}
	baseURI, ok := ports.VideoOutputBaseURIFromContext(ctx)
	if !ok {
		return video, nil
	}
	targetURI := buildVeoCanonicalVideoURI(baseURI, req.CutIndex)
	if targetURI == "" || targetURI == video.GCSURI {
		return video, nil
	}
	sourceURI := video.GCSURI
	if err := r.videoCopier.Copy(ctx, sourceURI, targetURI); err != nil {
		return vertexVideo{}, fmt.Errorf("copy generated video to canonical path: %w", err)
	}
	video.GCSURI = targetURI
	video.URI = targetURI
	if err := r.videoCopier.Delete(ctx, sourceURI); err != nil {
		slog.WarnContext(ctx, "failed to delete temporary Veo video after canonical copy",
			"source_uri", sourceURI,
			"target_uri", targetURI,
			"error", err,
		)
	}
	return video, nil
}

type videoCopier interface {
	Copy(ctx context.Context, sourceURI, targetURI string) error
	Delete(ctx context.Context, uri string) error
}

type gcsVideoCopier struct {
	client *storage.Client
}

// Close は copier が保持する GCS クライアントを解放します。
func (c *gcsVideoCopier) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	client := c.client
	c.client = nil
	return client.Close()
}

// Copy は GCS オブジェクトを指定 URI へコピーします。
func (c *gcsVideoCopier) Copy(ctx context.Context, sourceURI, targetURI string) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("GCS client is not configured")
	}
	sourceBucket, sourceObject, err := remoteio.ParseRemoteURI(sourceURI)
	if err != nil {
		return err
	}
	targetBucket, targetObject, err := remoteio.ParseRemoteURI(targetURI)
	if err != nil {
		return err
	}
	if sourceObject == "" || targetObject == "" {
		return fmt.Errorf("GCS object path is required")
	}
	_, err = c.client.Bucket(targetBucket).Object(targetObject).
		CopierFrom(c.client.Bucket(sourceBucket).Object(sourceObject)).
		Run(ctx)
	return err
}

// Delete は GCS オブジェクトを削除します。
func (c *gcsVideoCopier) Delete(ctx context.Context, uri string) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("GCS client is not configured")
	}
	bucket, object, err := remoteio.ParseRemoteURI(uri)
	if err != nil {
		return err
	}
	if object == "" {
		return fmt.Errorf("GCS object path is required")
	}
	return c.client.Bucket(bucket).Object(object).Delete(ctx)
}
