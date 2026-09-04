package step

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/ports"
)

// TestChainEndVideoURLsFindsBoundaries verifies that chain boundaries are detected from
// IsChainStart markers rather than duration_sec, matching how runDirect marks resets.
func TestChainEndVideoURLsFindsBoundaries(t *testing.T) {
	cuts := []video.Cut{
		{CutIndex: 1, VideoURL: "gs://bucket/cut_1.mp4", IsChainStart: true},
		{CutIndex: 2, VideoURL: "gs://bucket/cut_2.mp4"},
		{CutIndex: 3, VideoURL: "gs://bucket/cut_3.mp4"},
		{CutIndex: 4, VideoURL: "gs://bucket/cut_4.mp4"},
		{CutIndex: 5, VideoURL: "gs://bucket/cut_5.mp4", IsChainStart: true},
	}
	got := chainEndVideoURLs(cuts, true)
	want := []string{"gs://bucket/cut_4.mp4", "gs://bucket/cut_5.mp4"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("chainEndVideoURLs() mismatch (-want +got):\n%s", diff)
	}
}

// TestChainEndVideoURLsSingleChain verifies a job with no chain reset returns exactly the last cut.
func TestChainEndVideoURLsSingleChain(t *testing.T) {
	cuts := []video.Cut{
		{CutIndex: 1, VideoURL: "gs://bucket/cut_1.mp4", IsChainStart: true},
		{CutIndex: 2, VideoURL: "gs://bucket/cut_2.mp4"},
	}
	got := chainEndVideoURLs(cuts, true)
	want := []string{"gs://bucket/cut_2.mp4"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("chainEndVideoURLs() mismatch (-want +got):\n%s", diff)
	}
}

// TestChainEndVideoURLsJoinsEveryCutWithoutChaining verifies that with VEO_USE_PREVIOUS_VIDEO
// off every cut is concatenated. Cuts are then generated independently from their own keyframes
// and no cut contains the ones before it, so taking only chain ends would drop all but the last.
// IsChainStart is never set in that mode either (video_gen.go marks it inside a UsePreviousVideo
// branch), which is what made the finished video collapse to a single cut.
func TestChainEndVideoURLsJoinsEveryCutWithoutChaining(t *testing.T) {
	cuts := []video.Cut{
		{CutIndex: 1, VideoURL: "gs://bucket/cut_1.mp4"},
		{CutIndex: 2, VideoURL: "gs://bucket/cut_2.mp4"},
		{CutIndex: 3, VideoURL: "gs://bucket/cut_3.mp4"},
		{CutIndex: 4, VideoURL: "gs://bucket/cut_4.mp4"},
	}
	got := chainEndVideoURLs(cuts, false)
	want := []string{
		"gs://bucket/cut_1.mp4",
		"gs://bucket/cut_2.mp4",
		"gs://bucket/cut_3.mp4",
		"gs://bucket/cut_4.mp4",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("chainEndVideoURLs() mismatch (-want every cut +got):\n%s", diff)
	}
}

// TestChainEndVideoURLsIgnoresChainStartWithoutChaining verifies that a stale IsChainStart left
// on a recipe by an earlier chained run does not shrink the result once chaining is off. The two
// modes disagree about what the marker means, so the mode decides, not the persisted flag.
func TestChainEndVideoURLsIgnoresChainStartWithoutChaining(t *testing.T) {
	cuts := []video.Cut{
		{CutIndex: 1, VideoURL: "gs://bucket/cut_1.mp4", IsChainStart: true},
		{CutIndex: 2, VideoURL: "gs://bucket/cut_2.mp4"},
	}
	got := chainEndVideoURLs(cuts, false)
	want := []string{"gs://bucket/cut_1.mp4", "gs://bucket/cut_2.mp4"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("chainEndVideoURLs() mismatch (-want +got):\n%s", diff)
	}
}

// TestChainFinalizeStepConcatsEveryCutWithoutChaining verifies the wiring end to end: the
// step, not just the helper, joins all cuts when it is told chaining is off.
func TestChainFinalizeStepConcatsEveryCutWithoutChaining(t *testing.T) {
	recipe := &video.Recipe{
		Cuts: []video.Cut{
			{CutIndex: 1, VideoURL: "gs://bucket/cut_1.mp4"},
			{CutIndex: 2, VideoURL: "gs://bucket/cut_2.mp4"},
			{CutIndex: 3, VideoURL: "gs://bucket/cut_3.mp4"},
		},
	}
	vp := &recordingVideoProcessor{concatResult: "gs://bucket/jobs/job-1/videos/final.mp4"}

	err := (ChainFinalizeStep{VideoProcessor: vp}).Execute(context.Background(), &Context{
		VideoRecipe: recipe, OutputPath: "gs://bucket/jobs/job-1/",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(vp.concatCalls) != 1 {
		t.Fatalf("ConcatHardCut calls = %d, want 1", len(vp.concatCalls))
	}
	want := []string{"gs://bucket/cut_1.mp4", "gs://bucket/cut_2.mp4", "gs://bucket/cut_3.mp4"}
	if diff := cmp.Diff(want, vp.concatCalls[0].videoURIs); diff != "" {
		t.Fatalf("ConcatHardCut videoURIs mismatch (-want every cut +got):\n%s", diff)
	}
}

// TestChainFinalizeStepConcatsAndSetsFinalVideoURL verifies the step collects each chain's
// final cut, calls ConcatHardCut in chain order, and records the result on the recipe.
func TestChainFinalizeStepConcatsAndSetsFinalVideoURL(t *testing.T) {
	recipe := &video.Recipe{
		Cuts: []video.Cut{
			{CutIndex: 1, VideoURL: "gs://bucket/cut_1.mp4", IsChainStart: true},
			{CutIndex: 2, VideoURL: "gs://bucket/cut_2.mp4"},
			{CutIndex: 3, VideoURL: "gs://bucket/cut_3.mp4", IsChainStart: true},
		},
	}
	vp := &recordingVideoProcessor{concatResult: "gs://bucket/jobs/job-1/videos/final.mp4"}
	st := ChainFinalizeStep{VideoProcessor: vp, UsePreviousVideo: true}

	err := st.Execute(context.Background(), &Context{
		VideoRecipe: recipe,
		OutputPath:  "gs://bucket/jobs/job-1/",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(vp.concatCalls) != 1 {
		t.Fatalf("ConcatHardCut calls = %d, want 1", len(vp.concatCalls))
	}
	wantURLs := []string{"gs://bucket/cut_2.mp4", "gs://bucket/cut_3.mp4"}
	if diff := cmp.Diff(wantURLs, vp.concatCalls[0].videoURIs); diff != "" {
		t.Errorf("ConcatHardCut videoURIs mismatch (-want +got):\n%s", diff)
	}
	if vp.concatCalls[0].destURI != "gs://bucket/jobs/job-1/videos/final.mp4" {
		t.Errorf("ConcatHardCut destURI = %q", vp.concatCalls[0].destURI)
	}
	if recipe.FinalVideoURL != "gs://bucket/jobs/job-1/videos/final.mp4" {
		t.Errorf("FinalVideoURL = %q, want set from ConcatHardCut result", recipe.FinalVideoURL)
	}
}

// TestChainFinalizeStepNoopWithoutVideoProcessor verifies the step is a no-op (does not
// error and does not set FinalVideoURL) when no VideoProcessor is configured, e.g. images-only
// pipelines that never include this step in the first place, or a nil default.
func TestChainFinalizeStepNoopWithoutVideoProcessor(t *testing.T) {
	recipe := &video.Recipe{
		Cuts: []video.Cut{{CutIndex: 1, VideoURL: "gs://bucket/cut_1.mp4"}},
	}
	st := ChainFinalizeStep{}
	err := st.Execute(context.Background(), &Context{VideoRecipe: recipe, OutputPath: "gs://bucket/jobs/job-1/"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if recipe.FinalVideoURL != "" {
		t.Errorf("FinalVideoURL = %q, want empty when VideoProcessor is nil", recipe.FinalVideoURL)
	}
}

// recordingVideoProcessor is a fake ports.VideoProcessor that records calls for assertions.
type recordingVideoProcessor struct {
	extractCalls    []extractCall
	concatCalls     []concatCall
	colorMatchCalls []colorMatchCall

	extractResult    string
	concatResult     string
	colorMatchResult string

	probeCalls  []string
	probeResult ports.VideoStats
	probeErr    error
}

type extractCall struct {
	videoURI string
	destURI  string
}

type concatCall struct {
	videoURIs []string
	destURI   string
}

type colorMatchCall struct {
	videoURI          string
	referenceImageURI string
	destURI           string
}

func (p *recordingVideoProcessor) ExtractLastFrame(_ context.Context, videoURI, destURI string) (string, error) {
	p.extractCalls = append(p.extractCalls, extractCall{videoURI: videoURI, destURI: destURI})
	if p.extractResult != "" {
		return p.extractResult, nil
	}
	return destURI, nil
}

func (p *recordingVideoProcessor) ConcatHardCut(_ context.Context, videoURIs []string, destURI string) (string, error) {
	p.concatCalls = append(p.concatCalls, concatCall{videoURIs: append([]string(nil), videoURIs...), destURI: destURI})
	if p.concatResult != "" {
		return p.concatResult, nil
	}
	return destURI, nil
}

func (p *recordingVideoProcessor) Probe(_ context.Context, videoURI string) (ports.VideoStats, error) {
	p.probeCalls = append(p.probeCalls, videoURI)
	return p.probeResult, p.probeErr
}

func (p *recordingVideoProcessor) ColorMatchSaturation(_ context.Context, videoURI, referenceImageURI, destURI string) (string, error) {
	p.colorMatchCalls = append(p.colorMatchCalls, colorMatchCall{videoURI: videoURI, referenceImageURI: referenceImageURI, destURI: destURI})
	if p.colorMatchResult != "" {
		return p.colorMatchResult, nil
	}
	return destURI, nil
}

// TestChainFinalizeProbesTheFinalVideo verifies the concatenated result is measured. The recipe
// carries per-cut durations, but nothing checked that the finished file matches them until now.
func TestChainFinalizeProbesTheFinalVideo(t *testing.T) {
	vp := &recordingVideoProcessor{probeResult: ports.VideoStats{DurationSeconds: 20, HasAudio: true}}
	sc := &Context{
		OutputPath: "gs://bucket/jobs/job-1/",
		VideoRecipe: &video.Recipe{
			Cuts: []video.Cut{{
				CutIndex:     1,
				EndSec:       20,
				VideoURL:     "gs://bucket/cut_1.mp4",
				IsChainStart: true,
			}},
		}}

	if err := (ChainFinalizeStep{VideoProcessor: vp}).Execute(context.Background(), sc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(vp.probeCalls) != 1 {
		t.Fatalf("probe calls = %d, want 1", len(vp.probeCalls))
	}
	if vp.probeCalls[0] != sc.VideoRecipe.FinalVideoURL {
		t.Errorf("probed %q, want the final video %q", vp.probeCalls[0], sc.VideoRecipe.FinalVideoURL)
	}
}

// TestChainFinalizeSucceedsWhenProbeFails verifies a probe failure does not fail the job. The
// video is already generated and playable; measuring it is a check, not a gate.
func TestChainFinalizeSucceedsWhenProbeFails(t *testing.T) {
	vp := &recordingVideoProcessor{probeErr: errors.New("ffmpeg unavailable")}
	sc := &Context{
		OutputPath: "gs://bucket/jobs/job-1/",
		VideoRecipe: &video.Recipe{
			Cuts: []video.Cut{{
				CutIndex:     1,
				EndSec:       20,
				VideoURL:     "gs://bucket/cut_1.mp4",
				IsChainStart: true,
			}},
		}}

	if err := (ChainFinalizeStep{VideoProcessor: vp}).Execute(context.Background(), sc); err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if sc.VideoRecipe.FinalVideoURL == "" {
		t.Error("FinalVideoURL should still be set when the probe fails")
	}
}

// TestExpectedDurationSecondsUsesTheLastCutEnd verifies the expected total comes from the
// normalized timeline rather than summing durations again.
func TestExpectedDurationSecondsUsesTheLastCutEnd(t *testing.T) {
	recipe := &video.Recipe{
		Cuts: []video.Cut{
			{CutIndex: 1, EndSec: 8},
			{CutIndex: 2, EndSec: 21.5},
		},
	}

	if got := expectedDurationSeconds(recipe); got != 21.5 {
		t.Errorf("expectedDurationSeconds() = %v, want 21.5", got)
	}
	if got := expectedDurationSeconds(&video.Recipe{}); got != 0 {
		t.Errorf("expectedDurationSeconds() with no cuts = %v, want 0", got)
	}
}
