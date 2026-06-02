package adapters

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"path"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"ap-mv/internal/config"
	"ap-mv/internal/ports"
)

const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// VertexVeoRunner calls the Vertex AI Veo long-running video generation API.
type VertexVeoRunner struct {
	client           *http.Client
	projectID        string
	locationID       string
	model            string
	outputStorageURI string
	aspectRatio      string
	generateAudio    bool
	pollInterval     time.Duration
	operationTimeout time.Duration
}

func NewVertexVeoRunner(ctx context.Context, cfg *config.Config) (*VertexVeoRunner, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if strings.TrimSpace(cfg.ProjectID) == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID is required")
	}
	if strings.TrimSpace(cfg.LocationID) == "" {
		return nil, fmt.Errorf("GCP_LOCATION_ID is required")
	}
	if strings.TrimSpace(cfg.GCSBucket) == "" {
		return nil, fmt.Errorf("GCS_MUSIC_BUCKET is required")
	}
	ts, err := google.DefaultTokenSource(ctx, cloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("create Google ADC token source: %w", err)
	}

	pollInterval := cfg.VeoPollInterval
	if pollInterval <= 0 {
		pollInterval = 10 * time.Second
	}
	operationTimeout := cfg.VeoOperationTimeout
	if operationTimeout <= 0 {
		operationTimeout = 20 * time.Minute
	}

	model := strings.TrimSpace(cfg.VeoModel)
	if model == "" {
		model = config.DefaultVeoModel
	}
	aspectRatio := strings.TrimSpace(cfg.VeoAspectRatio)
	if aspectRatio == "" {
		aspectRatio = config.DefaultVeoAspect
	}

	return &VertexVeoRunner{
		client:           oauth2.NewClient(ctx, ts),
		projectID:        strings.TrimSpace(cfg.ProjectID),
		locationID:       strings.TrimSpace(cfg.LocationID),
		model:            model,
		outputStorageURI: buildVeoOutputStorageURI(cfg.GCSBucket, cfg.VeoOutputPrefix),
		aspectRatio:      aspectRatio,
		generateAudio:    cfg.VeoGenerateAudio,
		pollInterval:     pollInterval,
		operationTimeout: operationTimeout,
	}, nil
}

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

func (r *VertexVeoRunner) startOperation(ctx context.Context, req ports.VideoGenerationRequest) (*vertexOperation, error) {
	var op vertexOperation
	if err := r.postJSON(ctx, r.modelURL("predictLongRunning"), r.buildGenerateBody(req), &op); err != nil {
		return nil, fmt.Errorf("start Veo operation: %w", err)
	}
	if strings.TrimSpace(op.Name) == "" {
		return nil, fmt.Errorf("start Veo operation: response missing operation name")
	}
	return &op, nil
}

func (r *VertexVeoRunner) waitOperation(ctx context.Context, operationName string) (*vertexOperation, error) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		var op vertexOperation
		body := map[string]string{"operationName": operationName}
		if err := r.postJSON(ctx, r.modelURL("fetchPredictOperation"), body, &op); err != nil {
			return nil, fmt.Errorf("fetch Veo operation: %w", err)
		}
		if op.Done {
			if op.Error != nil {
				return nil, fmt.Errorf("Veo operation failed: %s", op.Error.message())
			}
			return &op, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *VertexVeoRunner) buildGenerateBody(req ports.VideoGenerationRequest) map[string]any {
	instance := map[string]any{
		"prompt": strings.TrimSpace(req.Prompt),
	}
	if media := previousVideoMedia(req.PreviousVideoID); media != nil {
		instance["video"] = media
	} else if media := imageMedia(req); media != nil {
		instance["image"] = media
	}

	parameters := map[string]any{
		"storageUri":      r.outputStorageURI,
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

func (r *VertexVeoRunner) postJSON(ctx context.Context, url string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (r *VertexVeoRunner) modelURL(method string) string {
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:%s",
		r.locationID, r.projectID, r.locationID, r.model, method)
}

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
		return fmt.Errorf("seed must be between 0 and %d", uint64(math.MaxUint32))
	}
	return nil
}

func buildVeoOutputStorageURI(bucket, prefix string) string {
	cleanPrefix := strings.Trim(path.Clean("/"+strings.TrimSpace(prefix)), "/")
	if cleanPrefix == "" || cleanPrefix == "." {
		cleanPrefix = config.DefaultVeoOutputRoot
	}
	return fmt.Sprintf("gs://%s/%s/", strings.TrimSpace(bucket), cleanPrefix)
}

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

func detectedImageMimeType(data []byte) string {
	mimeType := http.DetectContentType(data)
	switch mimeType {
	case "image/jpeg", "image/png", "image/webp":
		return mimeType
	default:
		return "image/png"
	}
}

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

type vertexOperation struct {
	Name     string           `json:"name"`
	Done     bool             `json:"done"`
	Error    *vertexError     `json:"error,omitempty"`
	Response *vertexVeoResult `json:"response,omitempty"`
}

type vertexError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func (e vertexError) message() string {
	parts := []string{e.Status, e.Message}
	if e.Code != 0 {
		parts = append([]string{fmt.Sprintf("code=%d", e.Code)}, parts...)
	}
	nonEmpty := parts[:0]
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, ": ")
}

type vertexVeoResult struct {
	Videos          []vertexVideo          `json:"videos,omitempty"`
	GeneratedVideos []vertexGeneratedVideo `json:"generatedVideos,omitempty"`
}

type vertexGeneratedVideo struct {
	Video vertexVideo `json:"video"`
}

type vertexVideo struct {
	GCSURI    string `json:"gcsUri,omitempty"`
	URI       string `json:"uri,omitempty"`
	MimeType  string `json:"mimeType,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

func firstGeneratedVideo(op *vertexOperation) (*vertexVideo, error) {
	if op.Response == nil {
		return nil, fmt.Errorf("Veo operation response is empty")
	}
	if len(op.Response.Videos) > 0 {
		video := op.Response.Videos[0]
		if video.GCSURI == "" {
			video.GCSURI = video.URI
		}
		return &video, nil
	}
	if len(op.Response.GeneratedVideos) > 0 {
		video := op.Response.GeneratedVideos[0].Video
		if video.GCSURI == "" {
			video.GCSURI = video.URI
		}
		return &video, nil
	}
	return nil, fmt.Errorf("Veo operation response contains no videos")
}
