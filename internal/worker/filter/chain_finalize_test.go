package filter

import (
	"context"
	"errors"
	"reflect"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/ports"
)

// TestChainEndVideoURLsFindsBoundaries verifies that chain boundaries are detected from
// IsChainStart markers rather than duration_sec, matching how runDirect marks resets.
func TestChainEndVideoURLsFindsBoundaries(t *testing.T) {
	cuts := []orchestrator.Cut{
		{CutIndex: 1, VideoResult: orchestrator.VideoResult{VideoURL: "gs://bucket/cut_1.mp4"}, ChainControl: orchestrator.ChainControl{IsChainStart: true}},
		{CutIndex: 2, VideoResult: orchestrator.VideoResult{VideoURL: "gs://bucket/cut_2.mp4"}},
		{CutIndex: 3, VideoResult: orchestrator.VideoResult{VideoURL: "gs://bucket/cut_3.mp4"}},
		{CutIndex: 4, VideoResult: orchestrator.VideoResult{VideoURL: "gs://bucket/cut_4.mp4"}},
		{CutIndex: 5, VideoResult: orchestrator.VideoResult{VideoURL: "gs://bucket/cut_5.mp4"}, ChainControl: orchestrator.ChainControl{IsChainStart: true}},
	}
	got := chainEndVideoURLs(cuts)
	want := []string{"gs://bucket/cut_4.mp4", "gs://bucket/cut_5.mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chainEndVideoURLs() = %v, want %v", got, want)
	}
}

// TestChainEndVideoURLsSingleChain verifies a job with no chain reset returns exactly the last cut.
func TestChainEndVideoURLsSingleChain(t *testing.T) {
	cuts := []orchestrator.Cut{
		{CutIndex: 1, VideoResult: orchestrator.VideoResult{VideoURL: "gs://bucket/cut_1.mp4"}, ChainControl: orchestrator.ChainControl{IsChainStart: true}},
		{CutIndex: 2, VideoResult: orchestrator.VideoResult{VideoURL: "gs://bucket/cut_2.mp4"}},
	}
	got := chainEndVideoURLs(cuts)
	want := []string{"gs://bucket/cut_2.mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chainEndVideoURLs() = %v, want %v", got, want)
	}
}

// TestChainFinalizeFilterConcatsAndSetsFinalVideoURL verifies the filter collects each chain's
// final cut, calls ConcatHardCut in chain order, and records the result on the recipe.
func TestChainFinalizeFilterConcatsAndSetsFinalVideoURL(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, VideoResult: orchestrator.VideoResult{VideoURL: "gs://bucket/cut_1.mp4"}, ChainControl: orchestrator.ChainControl{IsChainStart: true}},
			{CutIndex: 2, VideoResult: orchestrator.VideoResult{VideoURL: "gs://bucket/cut_2.mp4"}},
			{CutIndex: 3, VideoResult: orchestrator.VideoResult{VideoURL: "gs://bucket/cut_3.mp4"}, ChainControl: orchestrator.ChainControl{IsChainStart: true}},
		},
	}
	vp := &recordingVideoProcessor{concatResult: "gs://bucket/jobs/job-1/videos/final.mp4"}
	flt := ChainFinalizeFilter{VideoProcessor: vp}

	err := flt.Execute(context.Background(), &Context{
		State: State{
			VideoRecipe: recipe,
			OutputPath:  "gs://bucket/jobs/job-1/",
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(vp.concatCalls) != 1 {
		t.Fatalf("ConcatHardCut calls = %d, want 1", len(vp.concatCalls))
	}
	wantURLs := []string{"gs://bucket/cut_2.mp4", "gs://bucket/cut_3.mp4"}
	if !reflect.DeepEqual(vp.concatCalls[0].videoURIs, wantURLs) {
		t.Errorf("ConcatHardCut videoURIs = %v, want %v", vp.concatCalls[0].videoURIs, wantURLs)
	}
	if vp.concatCalls[0].destURI != "gs://bucket/jobs/job-1/videos/final.mp4" {
		t.Errorf("ConcatHardCut destURI = %q", vp.concatCalls[0].destURI)
	}
	if recipe.FinalVideoURL != "gs://bucket/jobs/job-1/videos/final.mp4" {
		t.Errorf("FinalVideoURL = %q, want set from ConcatHardCut result", recipe.FinalVideoURL)
	}
}

// TestChainFinalizeFilterNoopWithoutVideoProcessor verifies the filter is a no-op (does not
// error and does not set FinalVideoURL) when no VideoProcessor is configured, e.g. images-only
// pipelines that never include this filter in the first place, or a nil default.
func TestChainFinalizeFilterNoopWithoutVideoProcessor(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		Cuts: []orchestrator.Cut{{CutIndex: 1, VideoResult: orchestrator.VideoResult{VideoURL: "gs://bucket/cut_1.mp4"}}},
	}
	flt := ChainFinalizeFilter{}
	err := flt.Execute(context.Background(), &Context{State: State{VideoRecipe: recipe, OutputPath: "gs://bucket/jobs/job-1/"}})
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
	fc := &Context{State: State{
		OutputPath: "gs://bucket/jobs/job-1/",
		VideoRecipe: &orchestrator.VideoRecipe{
			Cuts: []orchestrator.Cut{{
				CutIndex:     1,
				AudioSync:    orchestrator.AudioSync{EndSec: 20},
				VideoResult:  orchestrator.VideoResult{VideoURL: "gs://bucket/cut_1.mp4"},
				ChainControl: orchestrator.ChainControl{IsChainStart: true},
			}},
		},
	}}

	if err := (ChainFinalizeFilter{VideoProcessor: vp}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(vp.probeCalls) != 1 {
		t.Fatalf("probe calls = %d, want 1", len(vp.probeCalls))
	}
	if vp.probeCalls[0] != fc.VideoRecipe.FinalVideoURL {
		t.Errorf("probed %q, want the final video %q", vp.probeCalls[0], fc.VideoRecipe.FinalVideoURL)
	}
}

// TestChainFinalizeSucceedsWhenProbeFails verifies a probe failure does not fail the job. The
// video is already generated and playable; measuring it is a check, not a gate.
func TestChainFinalizeSucceedsWhenProbeFails(t *testing.T) {
	vp := &recordingVideoProcessor{probeErr: errors.New("ffmpeg unavailable")}
	fc := &Context{State: State{
		OutputPath: "gs://bucket/jobs/job-1/",
		VideoRecipe: &orchestrator.VideoRecipe{
			Cuts: []orchestrator.Cut{{
				CutIndex:     1,
				AudioSync:    orchestrator.AudioSync{EndSec: 20},
				VideoResult:  orchestrator.VideoResult{VideoURL: "gs://bucket/cut_1.mp4"},
				ChainControl: orchestrator.ChainControl{IsChainStart: true},
			}},
		},
	}}

	if err := (ChainFinalizeFilter{VideoProcessor: vp}).Execute(context.Background(), fc); err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if fc.VideoRecipe.FinalVideoURL == "" {
		t.Error("FinalVideoURL should still be set when the probe fails")
	}
}

// TestExpectedDurationSecondsUsesTheLastCutEnd verifies the expected total comes from the
// normalized timeline rather than summing durations again.
func TestExpectedDurationSecondsUsesTheLastCutEnd(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		Cuts: []orchestrator.Cut{
			{CutIndex: 1, AudioSync: orchestrator.AudioSync{EndSec: 8}},
			{CutIndex: 2, AudioSync: orchestrator.AudioSync{EndSec: 21.5}},
		},
	}

	if got := expectedDurationSeconds(recipe); got != 21.5 {
		t.Errorf("expectedDurationSeconds() = %v, want 21.5", got)
	}
	if got := expectedDurationSeconds(&orchestrator.VideoRecipe{}); got != 0 {
		t.Errorf("expectedDurationSeconds() with no cuts = %v, want 0", got)
	}
}
