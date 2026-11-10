package step

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/ports"
)

// ChainFinalizeStep is the pipeline step that joins the continuation chains into one finished
// video with hard cuts after every cut is generated. It runs right after videoGen and right before
// PublishingStep, and is not part of commands without video generation (the image-only paths).
type ChainFinalizeStep struct {
	VideoProcessor ports.VideoProcessor
	// UsePreviousVideo must match VideoGenerationStep.UsePreviousVideo.
	// When false, cuts are not linked video-to-video and IsChainStart is never set, so how the
	// join targets are chosen changes (see chainEndVideoURLs).
	UsePreviousVideo bool
}

// Name returns the receiver name.
func (ChainFinalizeStep) Name() string { return "chain_finalize" }

// Execute runs the receiver processing step.
func (f ChainFinalizeStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil || sc.VideoRecipe == nil {
		return fmt.Errorf("chain finalize requires video recipe")
	}
	if f.VideoProcessor == nil {
		return nil
	}
	chainEndURLs := chainEndVideoURLs(sc.VideoRecipe.Cuts, f.UsePreviousVideo)
	if len(chainEndURLs) == 0 {
		return nil
	}
	destURI := finalVideoDestURI(sc.OutputPath)
	if destURI == "" {
		return nil
	}
	finalURL, err := f.VideoProcessor.ConcatHardCut(ctx, chainEndURLs, destURI)
	if err != nil {
		return fmt.Errorf("concat chains: %w", err)
	}
	sc.VideoRecipe.FinalVideoURL = finalURL
	f.inspectFinalVideo(ctx, sc.VideoRecipe, finalURL)
	return nil
}

// finalDurationToleranceSeconds is the allowed gap between the script's total length and the
// finished video's actual length.
//
// The length of clips Veo returns is never exactly what was requested, and re-encoding on the join
// moves the fractions too. A few seconds of gap is normal, so the width only catches obvious losses
// (missing cuts or shortening from failed generation).
const finalDurationToleranceSeconds = 5.0

// inspectFinalVideo measures the finished video and logs a warning when it disagrees with the script.
//
// It does not fail here. The video itself was generated and plays, so the decision to discard it
// is left to operations. Passing silently would leave breakage such as too short a length or no
// audio noticeable only "when the finished piece is played".
func (f ChainFinalizeStep) inspectFinalVideo(ctx context.Context, recipe *video.Recipe, finalURL string) {
	stats, err := f.VideoProcessor.Probe(ctx, finalURL)
	if err != nil {
		slog.WarnContext(ctx, "failed to analyse the final video", "url", finalURL, "err", err)
		return
	}

	slog.InfoContext(ctx, "analysed the final video",
		"url", finalURL, "duration_sec", stats.DurationSeconds, "has_audio", stats.HasAudio)

	if !stats.HasAudio {
		slog.WarnContext(ctx, "the final video has no audio track", "url", finalURL)
	}
	if expected := expectedDurationSeconds(recipe); expected > 0 {
		if diff := math.Abs(stats.DurationSeconds - expected); diff > finalDurationToleranceSeconds {
			slog.WarnContext(ctx, "the final video's length does not match the script",
				"url", finalURL, "expected_sec", expected, "actual_sec", stats.DurationSeconds, "diff_sec", diff)
		}
	}
}

// expectedDurationSeconds returns the total length the script expects.
// The last cut's EndSec is the value Normalize accumulated from each cut's duration.
func expectedDurationSeconds(recipe *video.Recipe) float64 {
	if recipe == nil || len(recipe.Cuts) == 0 {
		return 0
	}
	return recipe.Cuts[len(recipe.Cuts)-1].EndSec
}

// chainEndVideoURLs returns the URLs of the videos to join, in the order the cuts appear.
//
// When usePreviousVideo is true, cuts are generated chained video-to-video, and the video of a
// chain's last cut contains the whole chain. So only each chain's last cut is joined, and a chain
// boundary is where "the next cut is a new chain start (IsChainStart), or this is the last cut"
// (the counterpart of the IsChainStart marking in video_gen.go).
//
// When usePreviousVideo is false there are no chains, and each cut is a clip of about 8 seconds
// generated independently from its own keyframe. The path that sets IsChainStart is also inside
// usePreviousVideo, so nothing is marked. Sharing that check would leave only the last cut as a
// boundary, and the finished video would shrink to the last cut alone. Here every cut is joined.
func chainEndVideoURLs(cuts []video.Cut, usePreviousVideo bool) []string {
	var urls []string
	for i, cut := range cuts {
		isBoundary := !usePreviousVideo || i == len(cuts)-1 || cuts[i+1].IsChainStart
		if isBoundary && strings.TrimSpace(cut.VideoURL) != "" {
			urls = append(urls, cut.VideoURL)
		}
	}
	return urls
}

// finalVideoDestURI builds the destination URI of the joined finished video.
func finalVideoDestURI(outputPath string) string {
	outputPath = strings.TrimRight(strings.TrimSpace(outputPath), "/")
	if outputPath == "" {
		return ""
	}
	return outputPath + "/videos/final.mp4"
}
