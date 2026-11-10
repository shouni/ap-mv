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

// buildVeoOutputStorageURI builds the default GCS output directory Vertex AI Veo uses.
func buildVeoOutputStorageURI(bucket, prefix string) string {
	cleanPrefix := strings.Trim(path.Clean("/"+strings.TrimSpace(prefix)), "/")
	return fmt.Sprintf("gs://%s/%s/", strings.TrimSpace(bucket), cleanPrefix)
}

// outputStorageURIFor returns the Veo output directory used for the request.
//
// When the context has a per-job output base URI, Veo's output goes to a per-cut temporary
// directory so it can later be copied to a stable file name.
func (r *VertexVeoRunner) outputStorageURIFor(ctx context.Context, req ports.VideoGenerationRequest) string {
	if r == nil {
		return ""
	}
	if baseURI, ok := ports.VideoOutputBaseURIFromContext(ctx); ok {
		return buildVeoTemporaryCutOutputStorageURI(baseURI, req.CutIndex)
	}
	return r.outputStorageURI
}

// buildVeoTemporaryCutOutputStorageURI builds the per-cut temporary GCS directory for generation.
func buildVeoTemporaryCutOutputStorageURI(baseURI string, cutIndex int) string {
	baseURI = strings.TrimRight(strings.TrimSpace(baseURI), "/")
	if baseURI == "" {
		return ""
	}
	return fmt.Sprintf("%s/tmp/videos/cut_%02d/", baseURI, cutIndex)
}

// buildVeoCanonicalVideoURI builds the stable GCS URI of a generated cut video.
func buildVeoCanonicalVideoURI(baseURI string, cutIndex int) string {
	baseURI = strings.TrimRight(strings.TrimSpace(baseURI), "/")
	if baseURI == "" {
		return ""
	}
	return fmt.Sprintf("%s/videos/cut_%02d.mp4", baseURI, cutIndex)
}

// canonicalizeGeneratedVideo copies the generated video to a stable path under the job when there
// is a base URI, and returns that URI. Veo's output goes to a per-cut temporary directory, so this
// pins the URI kept in the metadata and the URI the later join refers to.
// With nothing to copy it returns sourceURI as-is.
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

// videoCopier represents only the operation needed to move outputs to their canonical path.
// remoteio.Store satisfies it as-is.
//
// This port used to hold a second GCS client and call CopierFrom itself. Since remoteio.Store.Copy
// falls back to a server-side copy within the same scheme, the injected store is enough.
type videoCopier interface {
	Copy(ctx context.Context, src, dst string, opts ...remoteio.WriteOption) error
	Delete(ctx context.Context, uri string) error
}
