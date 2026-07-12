package filter

import (
	"context"
	"reflect"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
)

// TestChainEndVideoURLsFindsBoundaries verifies that chain boundaries are detected from
// IsChainStart markers rather than duration_sec, matching how runDirect marks resets.
func TestChainEndVideoURLsFindsBoundaries(t *testing.T) {
	cuts := []orchestrator.Cut{
		{CutIndex: 1, VideoURL: "gs://bucket/cut_1.mp4", IsChainStart: true},
		{CutIndex: 2, VideoURL: "gs://bucket/cut_2.mp4"},
		{CutIndex: 3, VideoURL: "gs://bucket/cut_3.mp4"},
		{CutIndex: 4, VideoURL: "gs://bucket/cut_4.mp4"},
		{CutIndex: 5, VideoURL: "gs://bucket/cut_5.mp4", IsChainStart: true},
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
		{CutIndex: 1, VideoURL: "gs://bucket/cut_1.mp4", IsChainStart: true},
		{CutIndex: 2, VideoURL: "gs://bucket/cut_2.mp4"},
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
			{CutIndex: 1, VideoURL: "gs://bucket/cut_1.mp4", IsChainStart: true},
			{CutIndex: 2, VideoURL: "gs://bucket/cut_2.mp4"},
			{CutIndex: 3, VideoURL: "gs://bucket/cut_3.mp4", IsChainStart: true},
		},
	}
	vp := &recordingVideoProcessor{concatResult: "gs://bucket/jobs/job-1/videos/final.mp4"}
	flt := ChainFinalizeFilter{VideoProcessor: vp}

	err := flt.Execute(context.Background(), &Context{
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
		Cuts: []orchestrator.Cut{{CutIndex: 1, VideoURL: "gs://bucket/cut_1.mp4"}},
	}
	flt := ChainFinalizeFilter{}
	err := flt.Execute(context.Background(), &Context{VideoRecipe: recipe, OutputPath: "gs://bucket/jobs/job-1/"})
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

func (p *recordingVideoProcessor) ColorMatchSaturation(_ context.Context, videoURI, referenceImageURI, destURI string) (string, error) {
	p.colorMatchCalls = append(p.colorMatchCalls, colorMatchCall{videoURI: videoURI, referenceImageURI: referenceImageURI, destURI: destURI})
	if p.colorMatchResult != "" {
		return p.colorMatchResult, nil
	}
	return destURI, nil
}
