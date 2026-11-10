package adapters

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/ports"
)

// satavgPattern is the regular expression that extracts the average saturation (0-255 scale)
// printed by ffmpeg's signalstats filter with metadata=print.
var satavgPattern = regexp.MustCompile(`lavfi\.signalstats\.SATAVG=([0-9.]+)`)

// defaultFFmpegBinary is the ffmpeg executable name on PATH (the Dockerfile places a
// statically linked binary at /usr/local/bin/ffmpeg).
const defaultFFmpegBinary = "ffmpeg"

// FFmpegVideoProcessor implements ports.VideoProcessor with the local ffmpeg binary.
// It is the one boundary in ap-mv that processes video binaries (frame extraction, joining).
type FFmpegVideoProcessor struct {
	Reader orchestrator.ContentReader
	Writer remoteio.Writer
	// Binary is the path to the ffmpeg executable. When empty, "ffmpeg" on PATH is used.
	Binary string
}

var _ ports.VideoProcessor = (*FFmpegVideoProcessor)(nil)

// NewFFmpegVideoProcessor initialises an FFmpegVideoProcessor.
func NewFFmpegVideoProcessor(reader orchestrator.ContentReader, writer remoteio.Writer) *FFmpegVideoProcessor {
	return &FFmpegVideoProcessor{Reader: reader, Writer: writer}
}

func (p *FFmpegVideoProcessor) binary() string {
	if p != nil && strings.TrimSpace(p.Binary) != "" {
		return p.Binary
	}
	return defaultFFmpegBinary
}

// ExtractLastFrame extracts the last frame of videoURI as an image and uploads it to destURI.
func (p *FFmpegVideoProcessor) ExtractLastFrame(ctx context.Context, videoURI, destURI string) (string, error) {
	if p == nil || p.Reader == nil || p.Writer == nil {
		return "", fmt.Errorf("ffmpeg video processor is not configured")
	}
	localVideo, err := p.downloadToTemp(ctx, videoURI, ".mp4")
	if err != nil {
		return "", fmt.Errorf("extract last frame: download %s: %w", videoURI, err)
	}
	defer func() { _ = os.Remove(localVideo) }()

	localFrame, err := tempFilePath(".png")
	if err != nil {
		return "", fmt.Errorf("extract last frame: %w", err)
	}
	defer func() { _ = os.Remove(localFrame) }()

	// -sseof -3: start decoding 3 seconds before the end (faster than decoding the whole file).
	// With -frames:v 1, the first frame decoded right after the seek (up to 3 seconds before the
	// end) is written, which is not the real last frame. Checked on real jobs, this drift was a
	// non-negligible difference of about 17 dB PSNR even with the seek at 1 second before the end.
	// Use -update 1 (the image2 muxer's single-file overwrite mode) so that it finishes with the
	// last of the frames decoded over the remaining seconds after the seek (= the true last frame)
	// written. Output PNG (lossless). The video itself is already lossily compressed as H.264, and
	// converting further to a lossy format such as JPEG here would add double-compression
	// degradation to the next chain's reference image, so that is avoided.
	cmd := exec.CommandContext(ctx, p.binary(), "-y", "-sseof", "-3", "-i", localVideo, "-update", "1", localFrame)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("extract last frame: ffmpeg: %w: %s", runErr, out)
	}

	if err := p.uploadFromTemp(ctx, localFrame, destURI, "image/png"); err != nil {
		return "", fmt.Errorf("extract last frame: upload %s: %w", destURI, err)
	}
	return destURI, nil
}

// ColorMatchSaturation corrects videoURI's saturation to match the average saturation of referenceImageURI.
func (p *FFmpegVideoProcessor) ColorMatchSaturation(ctx context.Context, videoURI, referenceImageURI, destURI string) (string, error) {
	if p == nil || p.Reader == nil || p.Writer == nil {
		return "", fmt.Errorf("ffmpeg video processor is not configured")
	}
	localVideo, err := p.downloadToTemp(ctx, videoURI, ".mp4")
	if err != nil {
		return "", fmt.Errorf("color match saturation: download video %s: %w", videoURI, err)
	}
	defer func() { _ = os.Remove(localVideo) }()

	localRef, err := p.downloadToTemp(ctx, referenceImageURI, ".png")
	if err != nil {
		return "", fmt.Errorf("color match saturation: download reference %s: %w", referenceImageURI, err)
	}
	defer func() { _ = os.Remove(localRef) }()

	refSat, err := p.averageSaturation(ctx, localRef)
	if err != nil {
		return "", fmt.Errorf("color match saturation: measure reference: %w", err)
	}
	// Sample the video near its end. video_extension regenerates the whole cumulative video each
	// time, so the first frame is an older span not newly generated (on that run) and may not have
	// accumulated drift. Drift appears on the end side newly generated in each generation, so not
	// measuring there underestimates the actual drift (measured: on the same video the first frame
	// had SATAVG=26.7 against 35.7 one second before the end; drift was detectable only at the end).
	videoSat, err := p.averageSaturationNearEnd(ctx, localVideo)
	if err != nil {
		return "", fmt.Errorf("color match saturation: measure video: %w", err)
	}
	if videoSat <= 0 {
		return "", fmt.Errorf("color match saturation: video has no measurable saturation")
	}

	// The correction ratio. Sampling one reference image and one video frame is a rough
	// measurement, so clamp to [0.7, 1.3] to avoid an unnatural look from overcorrecting on error or composition differences.
	ratio := refSat / videoSat
	if ratio < 0.7 {
		ratio = 0.7
	} else if ratio > 1.3 {
		ratio = 1.3
	}

	localOut, err := tempFilePath(".mp4")
	if err != nil {
		return "", fmt.Errorf("color match saturation: %w", err)
	}
	defer func() { _ = os.Remove(localOut) }()

	// eq=saturation=<ratio> scales every frame's saturation uniformly. Audio is copied as-is
	// without re-encoding (only the video filter applies).
	cmd := exec.CommandContext(ctx, p.binary(), "-y", "-i", localVideo,
		"-vf", fmt.Sprintf("eq=saturation=%.4f", ratio),
		"-c:v", "libx264", "-preset", "fast", "-crf", "18", "-c:a", "copy", localOut)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("color match saturation: ffmpeg encode: %w: %s", runErr, out)
	}

	if err := p.uploadFromTemp(ctx, localOut, destURI, "video/mp4"); err != nil {
		return "", fmt.Errorf("color match saturation: upload %s: %w", destURI, err)
	}
	return destURI, nil
}

// averageSaturation measures the average saturation (SATAVG, 0-255 scale) of the first frame
// of an image (or a seeked video) with ffmpeg's signalstats filter.
func (p *FFmpegVideoProcessor) averageSaturation(ctx context.Context, localPath string) (float64, error) {
	return p.averageSaturationArgs(ctx, "-i", localPath, "-frames:v", "1", "-vf", "signalstats,metadata=print", "-f", "null", "-")
}

// averageSaturationNearEnd measures the average saturation of the frame one second before the video's end.
func (p *FFmpegVideoProcessor) averageSaturationNearEnd(ctx context.Context, localVideo string) (float64, error) {
	return p.averageSaturationArgs(ctx, "-sseof", "-1", "-i", localVideo, "-frames:v", "1", "-vf", "signalstats,metadata=print", "-f", "null", "-")
}

func (p *FFmpegVideoProcessor) averageSaturationArgs(ctx context.Context, args ...string) (float64, error) {
	cmd := exec.CommandContext(ctx, p.binary(), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("ffmpeg signalstats: %w: %s", err, out)
	}
	match := satavgPattern.FindSubmatch(out)
	if match == nil {
		return 0, fmt.Errorf("SATAVG not found in ffmpeg signalstats output")
	}
	sat, err := strconv.ParseFloat(string(match[1]), 64)
	if err != nil {
		return 0, fmt.Errorf("parse SATAVG %q: %w", match[1], err)
	}
	return sat, nil
}

// ConcatHardCut joins videoURIs with hard cuts and uploads the result to destURI.
// With a single input it is copied as-is without re-encoding.
func (p *FFmpegVideoProcessor) ConcatHardCut(ctx context.Context, videoURIs []string, destURI string) (string, error) {
	if p == nil || p.Reader == nil || p.Writer == nil {
		return "", fmt.Errorf("ffmpeg video processor is not configured")
	}
	if len(videoURIs) == 0 {
		return "", fmt.Errorf("concat hard cut: no input videos")
	}
	if len(videoURIs) == 1 {
		if err := p.streamCopy(ctx, videoURIs[0], destURI, "video/mp4"); err != nil {
			return "", fmt.Errorf("concat hard cut: copy single video: %w", err)
		}
		return destURI, nil
	}

	localPaths := make([]string, 0, len(videoURIs))
	defer func() {
		for _, lp := range localPaths {
			_ = os.Remove(lp)
		}
	}()
	for i, uri := range videoURIs {
		localPath, err := p.downloadToTemp(ctx, uri, ".mp4")
		if err != nil {
			return "", fmt.Errorf("concat hard cut: download chain %d (%s): %w", i, uri, err)
		}
		localPaths = append(localPaths, localPath)
	}

	listFile, err := writeConcatList(localPaths)
	if err != nil {
		return "", fmt.Errorf("concat hard cut: %w", err)
	}
	defer func() { _ = os.Remove(listFile) }()

	localOut, err := tempFilePath(".mp4")
	if err != nil {
		return "", fmt.Errorf("concat hard cut: %w", err)
	}
	defer func() { _ = os.Remove(localOut) }()

	// Each chain is a clip made by a separate Veo generation call, so "-c copy", which assumes
	// matching codecs/parameters, is not used; it always re-encodes (clips are a few tens of
	// seconds, so the cost is small).
	cmd := exec.CommandContext(ctx, p.binary(),
		"-y", "-f", "concat", "-safe", "0", "-i", listFile,
		"-c:v", "libx264", "-preset", "veryfast", "-c:a", "aac",
		localOut,
	)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("concat hard cut: ffmpeg: %w: %s", runErr, out)
	}

	if err := p.uploadFromTemp(ctx, localOut, destURI, "video/mp4"); err != nil {
		return "", fmt.Errorf("concat hard cut: upload %s: %w", destURI, err)
	}
	return destURI, nil
}

// downloadToTemp streams the contents of srcURI into a local temp file and returns its path.
// The caller is responsible for removing it with os.Remove.
func (p *FFmpegVideoProcessor) downloadToTemp(ctx context.Context, srcURI, ext string) (string, error) {
	rc, err := p.Reader.Open(ctx, srcURI)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", srcURI, err)
	}
	defer func() { _ = rc.Close() }()

	f, err := os.CreateTemp("", "ap-mv-videoproc-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	path := f.Name()
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, rc); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("write temp file: %w", err)
	}
	return path, nil
}

// uploadFromTemp uploads the contents of the local file localPath to destURI.
func (p *FFmpegVideoProcessor) uploadFromTemp(ctx context.Context, localPath, destURI, contentType string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", localPath, err)
	}
	defer func() { _ = f.Close() }()
	return p.Writer.Write(ctx, destURI, f, remoteio.WithContentType(contentType))
}

// streamCopy stream-copies the contents of srcURI straight to destURI without writing them locally.
func (p *FFmpegVideoProcessor) streamCopy(ctx context.Context, srcURI, destURI, contentType string) error {
	rc, err := p.Reader.Open(ctx, srcURI)
	if err != nil {
		return fmt.Errorf("open %s: %w", srcURI, err)
	}
	defer func() { _ = rc.Close() }()
	return p.Writer.Write(ctx, destURI, rc, remoteio.WithContentType(contentType))
}

// tempFilePath creates an empty temp file and returns its path (the file handle is closed).
func tempFilePath(ext string) (string, error) {
	f, err := os.CreateTemp("", "ap-mv-videoproc-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close temp file: %w", err)
	}
	return path, nil
}

// writeConcatList creates the list file for ffmpeg's concat demuxer and returns its path.
func writeConcatList(localPaths []string) (string, error) {
	f, err := os.CreateTemp("", "ap-mv-videoproc-*.txt")
	if err != nil {
		return "", fmt.Errorf("create concat list: %w", err)
	}
	listPath := f.Name()
	defer func() { _ = f.Close() }()
	var sb strings.Builder
	for _, p := range localPaths {
		// The concat demuxer escapes single quotes as '\''
		escaped := strings.ReplaceAll(p, "'", `'\''`)
		fmt.Fprintf(&sb, "file '%s'\n", escaped)
	}
	if _, err := f.WriteString(sb.String()); err != nil {
		_ = os.Remove(listPath)
		return "", fmt.Errorf("write concat list: %w", err)
	}
	return listPath, nil
}

// probeStreamsPattern picks the stream type from ffmpeg's stream lines.
var probeStreamsPattern = regexp.MustCompile(`Stream #\d+:\d+.*?: (Video|Audio):`)

// probeDurationPattern picks the total duration (HH:MM:SS.ss) from ffmpeg's header output.
var probeDurationPattern = regexp.MustCompile(`Duration: (\d+):(\d+):(\d+\.\d+)`)

// Probe measures a video's actual duration and whether it has an audio track.
//
// ffmpeg is used rather than ffprobe because ffmpeg is the only binary in this container.
// The output is sent to null so it only analyses, and the header information on stderr is read.
func (p *FFmpegVideoProcessor) Probe(ctx context.Context, videoURI string) (ports.VideoStats, error) {
	if p == nil || p.Reader == nil {
		return ports.VideoStats{}, fmt.Errorf("ffmpeg video processor is not configured")
	}

	localPath, err := p.downloadToTemp(ctx, videoURI, ".mp4")
	if err != nil {
		return ports.VideoStats{}, fmt.Errorf("probe: download %s: %w", videoURI, err)
	}
	defer func() { _ = os.Remove(localPath) }()

	// Only analyse, so discard the output. Running with just -i exits with code 1, so decide
	// by the content of the output rather than by the error.
	cmd := exec.CommandContext(ctx, p.binary(), "-i", localPath, "-f", "null", "-")
	out, _ := cmd.CombinedOutput()

	stats, err := parseProbeOutput(out)
	if err != nil {
		return ports.VideoStats{}, fmt.Errorf("probe %s: %w", videoURI, err)
	}
	return stats, nil
}

// parseProbeOutput reads the duration and whether there is an audio track from ffmpeg's header output.
// It is a pure function separated from running ffmpeg so the parsing rules can be tested
// without the binary.
func parseProbeOutput(out []byte) (ports.VideoStats, error) {
	stats := ports.VideoStats{}
	for _, match := range probeStreamsPattern.FindAllSubmatch(out, -1) {
		if string(match[1]) == "Audio" {
			stats.HasAudio = true
		}
	}

	match := probeDurationPattern.FindSubmatch(out)
	if match == nil {
		return ports.VideoStats{}, fmt.Errorf("duration not found in ffmpeg output")
	}
	hours, _ := strconv.ParseFloat(string(match[1]), 64)
	minutes, _ := strconv.ParseFloat(string(match[2]), 64)
	seconds, err := strconv.ParseFloat(string(match[3]), 64)
	if err != nil {
		return ports.VideoStats{}, fmt.Errorf("parse duration %q: %w", match[0], err)
	}
	stats.DurationSeconds = hours*3600 + minutes*60 + seconds

	return stats, nil
}
