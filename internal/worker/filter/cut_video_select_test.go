package filter

import (
	"context"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
)

// generatedCut builds a cut in the state a finished job leaves behind.
func generatedCut(index int, durationSec float64, chainStart bool) orchestrator.Cut {
	return orchestrator.Cut{
		CutIndex:       index,
		SectionIndex:   1,
		VisualAnchor:   "anchor",
		AudioSync:      orchestrator.AudioSync{DurationSec: durationSec},
		KeyframeResult: orchestrator.KeyframeResult{KeyframeReference: "gs://bucket/jobs/job-1/images/keyframe.png"},
		ChainControl:   orchestrator.ChainControl{IsChainStart: chainStart},
		VideoResult: orchestrator.VideoResult{
			VideoID:  "video-" + string(rune('0'+index)),
			VideoURL: "gs://bucket/jobs/job-1/videos/cut.mp4",
			Status:   orchestrator.CutStatusGenerated,
		},
	}
}

// chainTestRecipe lays out two chains: cuts 1-3 (8s base + two 7s extensions) and cuts 4-5
// (8s base + one 7s extension).
func chainTestRecipe() *orchestrator.VideoRecipe {
	return &orchestrator.VideoRecipe{
		ProjectTitle: "test",
		MusicRecipe:  orchestrator.MusicRecipe{Title: "test"},
		Cuts: []orchestrator.Cut{
			generatedCut(1, 8, true),
			generatedCut(2, veoVideoExtensionDurationSec, false),
			generatedCut(3, veoVideoExtensionDurationSec, false),
			generatedCut(4, 8, true),
			generatedCut(5, veoVideoExtensionDurationSec, false),
		},
	}
}

func runCutVideoSelect(t *testing.T, recipe *orchestrator.VideoRecipe, cutIndex int, usePreviousVideo bool) error {
	t.Helper()
	return CutVideoSelectFilter{UsePreviousVideo: usePreviousVideo}.Execute(context.Background(), &Context{
		State: State{
			Task: &domain.Task{
				JobID:     "mv-2",
				Command:   domain.CommandRegenerateCutVideo,
				CutIndex:  &cutIndex,
				RecipeURL: "gs://bucket/jobs/job-1/video_music_meta.json",
			},
			VideoRecipe: recipe,
		},
	})
}

func pendingCutIndexes(recipe *orchestrator.VideoRecipe) []int {
	var pending []int
	for _, cut := range recipe.Cuts {
		if !cut.IsGenerated() {
			pending = append(pending, cut.CutIndex)
		}
	}
	return pending
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCutVideoSelectResetsRestOfChain is the point of the filter: in a video-to-video chain, a
// regenerated cut invalidates every later cut in the same chain, because those were generated
// against the old cut's video as PreviousVideoID. Resetting only the target would leave the tail
// chained to a video that no longer exists in the result.
func TestCutVideoSelectResetsRestOfChain(t *testing.T) {
	recipe := chainTestRecipe()
	if err := runCutVideoSelect(t, recipe, 2, true); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := pendingCutIndexes(recipe), []int{2, 3}; !equalInts(got, want) {
		t.Errorf("pending cuts = %v, want %v (target plus the rest of its chain)", got, want)
	}
}

// TestCutVideoSelectStopsAtNextChainBase pins that the reset does not run past the chain
// boundary: the next chain does not consume the regenerated chain's video, so re-generating it
// would be paid-for waste.
func TestCutVideoSelectStopsAtNextChainBase(t *testing.T) {
	recipe := chainTestRecipe()
	if err := runCutVideoSelect(t, recipe, 3, true); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := pendingCutIndexes(recipe), []int{3}; !equalInts(got, want) {
		t.Errorf("pending cuts = %v, want %v (last cut of its chain closes alone)", got, want)
	}
}

// TestCutVideoSelectChainBaseResetsWholeChain covers targeting the base of a chain.
func TestCutVideoSelectChainBaseResetsWholeChain(t *testing.T) {
	recipe := chainTestRecipe()
	if err := runCutVideoSelect(t, recipe, 4, true); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := pendingCutIndexes(recipe), []int{4, 5}; !equalInts(got, want) {
		t.Errorf("pending cuts = %v, want %v", got, want)
	}
}

// TestCutVideoSelectWithoutPreviousVideoResetsOneCut pins the image-anchor mode: cuts are not
// chained through video, so exactly one cut is regenerated.
func TestCutVideoSelectWithoutPreviousVideoResetsOneCut(t *testing.T) {
	recipe := chainTestRecipe()
	if err := runCutVideoSelect(t, recipe, 2, false); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := pendingCutIndexes(recipe), []int{2}; !equalInts(got, want) {
		t.Errorf("pending cuts = %v, want %v", got, want)
	}
}

// TestCutVideoSelectKeepsAllCutsAndKeyframes pins two things the filter must not do: drop the
// untouched cuts (chain finalize concatenates every cut, so trimming would yield a short video
// instead of a repaired full MV) and drop the keyframes (only the video is being redone).
func TestCutVideoSelectKeepsAllCutsAndKeyframes(t *testing.T) {
	recipe := chainTestRecipe()
	if err := runCutVideoSelect(t, recipe, 2, true); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(recipe.Cuts) != 5 {
		t.Fatalf("cuts = %d, want all 5 kept", len(recipe.Cuts))
	}
	for i, cut := range recipe.Cuts {
		if cut.KeyframeReference == "" {
			t.Errorf("cut[%d] lost its keyframe reference; only the video should be regenerated", i)
		}
	}
	// 作り直さないカットは動画をそのまま保持し、videoGen にスキップさせる。
	if recipe.Cuts[0].VideoID == "" || !recipe.Cuts[0].IsGenerated() {
		t.Error("cut 1 was reset; untouched cuts must keep their generated video")
	}
}

// TestCutVideoSelectResolvesRelativeKeyframes pins that a stored recipe's job-relative keyframe
// paths are absolutised against the original job, since the regenerated cuts run under a new job
// whose output path would not resolve them.
func TestCutVideoSelectResolvesRelativeKeyframes(t *testing.T) {
	recipe := chainTestRecipe()
	recipe.Cuts[1].KeyframeReference = "images/keyframe_002.png"

	if err := runCutVideoSelect(t, recipe, 2, true); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "gs://bucket/jobs/job-1/images/keyframe_002.png"
	if got := recipe.Cuts[1].KeyframeReference; got != want {
		t.Errorf("KeyframeReference = %q, want %q", got, want)
	}
}

func TestCutVideoSelectRejectsUnknownCutIndex(t *testing.T) {
	recipe := chainTestRecipe()
	if err := runCutVideoSelect(t, recipe, 99, true); err == nil {
		t.Fatal("Execute() error = nil, want an error for a cut_index not in the recipe")
	}
}
