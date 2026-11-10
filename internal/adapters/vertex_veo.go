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

// VertexVeoRunner is the Runner that calls Vertex AI Veo's video generation.
//
// API calls and polling belong to genai-kit's veo package; this type handles only the app's own
// concerns: deciding the per-job output location, copying outputs to canonical paths under the
// job, and replacing the model and aspect ratio per task.
type VertexVeoRunner struct {
	videos           *veo.Client
	videoCopier      videoCopier
	model            string
	outputStorageURI string
	aspectRatio      string
	generateAudio    bool
	usePreviousVideo bool
}

// Close only lets go of references.
//
// It used to close a dedicated GCS client here. Now the store is injected, so its lifetime belongs
// to whoever injected it (the factory).
func (r *VertexVeoRunner) Close() error {
	if r == nil {
		return nil
	}
	r.videoCopier = nil
	return nil
}

// NewVertexVeoRunner creates a VertexVeoRunner from the application settings.
//
// aiClient injects the Vertex AI client built by NewVertexAIAdapter. It does not build its own
// because if text/image generation and video generation each had a client, the region, retry
// interval and HTTP timeout could change on one side only and disagree (InitialDelay actually split
// into 60s and 30s). This gathers authentication and retry settings in one place.
//
// store is used to move videos from Veo's temporary output path to the canonical path under the
// job. A second GCS client used to be created here, but it became unnecessary once
// remoteio.Store.Copy fell back to a server-side copy within the same scheme.
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

// WithVideoOptions returns a derived Runner with only the model and aspect ratio replaced.
// The veo client and the store are shared, so it is safe to call per task.
// An empty string keeps the original setting.
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

// Run runs Veo's video generation and, when done, returns the generated video's metadata.
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
		return nil, fmt.Errorf("cut %d: the generation result contains no video (operation: %s)", req.CutIndex, result.OperationName)
	}

	cloudURL, err := r.canonicalizeGeneratedVideo(ctx, req, video.URI)
	if err != nil {
		return nil, err
	}

	// VideoID is used as the next cut's PreviousVideoURI, so only a referenceable gs:// URI goes in
	// (VideoResponse.VideoID's contract). It used to fall back to the operation name when there was
	// no URI, but that value, though not classified as video_extension, was carried around as chain
	// context, and cuts planned at 7 seconds were wrongly rejected by the duration check.
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
