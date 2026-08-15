package filter

import (
	"context"
	"strings"
	"testing"

	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

func TestSceneSplitFilterExpandsLongCutsBeforeKeyframeGeneration(t *testing.T) {
	recipe := &video.Recipe{
		ProjectTitle: "test",
		MusicRecipe: video.MusicRecipe{
			Title: "test",
			Sections: []video.Section{{
				Name:         "Chorus",
				Duration:     20,
				StartSeconds: 0,
				EndSeconds:   20,
				Prompt:       "chorus lift",
			}},
		},
		Cuts: []video.Cut{{
			CutIndex:       1,
			VisualAnchor:   "protagonist on a glowing rooftop",
			Dialogue:       "line one\nline two\nline three",
			AudioSync:      video.AudioSync{DurationSec: 20, AudioCue: "chorus lift"},
			KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://bucket/old.png"},
			Result: video.Result{
				VideoID:  "old-video",
				VideoURL: "gs://bucket/old.mp4",
				Status:   video.CutStatusGenerated,
			},
		}},
	}

	err := (SceneSplitFilter{}).Execute(context.Background(), &Context{
		State: State{
			Task:        &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate},
			VideoRecipe: recipe,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(recipe.Cuts) != 3 {
		t.Fatalf("cuts = %d, want 3", len(recipe.Cuts))
	}
	wantDurations := []float64{8, 8, 4}
	for i, cut := range recipe.Cuts {
		if cut.CutIndex != i+1 {
			t.Errorf("cut[%d].CutIndex = %d, want %d", i, cut.CutIndex, i+1)
		}
		if cut.DurationSec != wantDurations[i] {
			t.Errorf("cut[%d].DurationSec = %v, want %v", i, cut.DurationSec, wantDurations[i])
		}
		if cut.Status != video.CutStatusPending {
			t.Errorf("cut[%d].Status = %q, want pending", i, cut.Status)
		}
		if cut.KeyframeReference != "" || cut.VideoID != "" || cut.VideoURL != "" {
			t.Errorf("cut[%d] generation fields were not reset: keyframe=%q videoID=%q videoURL=%q", i, cut.KeyframeReference, cut.VideoID, cut.VideoURL)
		}
		if !strings.Contains(cut.VisualAnchor, "Scene beat") {
			t.Errorf("cut[%d].VisualAnchor = %q, want scene beat direction", i, cut.VisualAnchor)
		}
	}
	if recipe.Cuts[0].VisualAnchor == recipe.Cuts[1].VisualAnchor {
		t.Fatal("split cuts reused the same visual anchor; want distinct keyframe direction")
	}
}

func TestSceneSplitFilterAllocatesVideoToVideoChainBlocks(t *testing.T) {
	recipe := &video.Recipe{
		ProjectTitle: "test",
		MusicRecipe:  video.MusicRecipe{Title: "test"},
		Cuts: []video.Cut{{
			CutIndex:     1,
			VisualAnchor: "protagonist crossing a luminous city stage",
			Dialogue:     "a\nb\nc\nd\ne",
			AudioSync:    video.AudioSync{DurationSec: 50, AudioCue: "long chorus"},
		}},
	}

	err := (SceneSplitFilter{UsePreviousVideo: true}).Execute(context.Background(), &Context{
		State: State{
			Task:        &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate},
			VideoRecipe: recipe,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// 50s allocates exactly (6+22+22=50) instead of the old round-up {15,15,22}=52s.
	wantDurations := []float64{6, 22, 22}
	if len(recipe.Cuts) != len(wantDurations) {
		t.Fatalf("cuts = %d, want %d", len(recipe.Cuts), len(wantDurations))
	}
	total := 0.0
	for i, want := range wantDurations {
		if recipe.Cuts[i].DurationSec != want {
			t.Errorf("cut[%d].DurationSec = %v, want %v", i, recipe.Cuts[i].DurationSec, want)
		}
		if !recipe.Cuts[i].IsChainStart {
			t.Errorf("cut[%d].IsChainStart = false, want true for a planned chain block", i)
		}
		if i == 0 && recipe.Cuts[i].IsSectionStart {
			t.Errorf("cut[0].IsSectionStart = true, want false")
		}
		if i > 0 && !recipe.Cuts[i].IsSectionStart {
			t.Errorf("cut[%d].IsSectionStart = false, want true for a new scene/keyframe block", i)
		}
		if recipe.Cuts[i].StartSec != total {
			t.Errorf("cut[%d].StartSec = %v, want %v (contiguous timeline)", i, recipe.Cuts[i].StartSec, total)
		}
		total += want
		if recipe.Cuts[i].EndSec != total {
			t.Errorf("cut[%d].EndSec = %v, want %v", i, recipe.Cuts[i].EndSec, total)
		}
	}
	if total != 50 {
		t.Errorf("total duration = %v, want exactly 50 (no round-up overage)", total)
	}
}

// TestSceneSplitFilterVideoToVideoTracksMusicTimeline reproduces the real-job bug where two
// lyric-line cuts of 9s and 10s were each rounded up to 15s blocks while keeping their original
// StartSec, so cut 1's end (28+15=43) overshot cut 2's start (37) by 6 seconds. With
// error-diffusing allocation the pair must cover its 19 musical seconds exactly and stay
// contiguous on the concatenated-video timeline.
func TestSceneSplitFilterVideoToVideoTracksMusicTimeline(t *testing.T) {
	recipe := &video.Recipe{
		ProjectTitle: "test",
		MusicRecipe:  video.MusicRecipe{Title: "test"},
		Cuts: []video.Cut{
			{CutIndex: 1, Dialogue: "line five", AudioSync: video.AudioSync{StartSec: 28, DurationSec: 9, AudioCue: "0:28 to 0:37"}},
			{CutIndex: 2, Dialogue: "line six", AudioSync: video.AudioSync{StartSec: 37, DurationSec: 10, AudioCue: "0:37 to 0:47"}},
		},
	}

	err := (SceneSplitFilter{UsePreviousVideo: true}).Execute(context.Background(), &Context{
		State: State{
			Task:        &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate},
			VideoRecipe: recipe,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// 9s rounds DOWN to the nearest chain length (8), and the 1s deficit is diffused into the
	// next cut's target (10+1=11), which the {4,6,8}+7k chain candidates hit exactly.
	wantDurations := []float64{8, 11}
	if len(recipe.Cuts) != len(wantDurations) {
		t.Fatalf("cuts = %d, want %d", len(recipe.Cuts), len(wantDurations))
	}
	for i, want := range wantDurations {
		if recipe.Cuts[i].DurationSec != want {
			t.Errorf("cut[%d].DurationSec = %v, want %v", i, recipe.Cuts[i].DurationSec, want)
		}
	}
	for i := 1; i < len(recipe.Cuts); i++ {
		if recipe.Cuts[i].StartSec != recipe.Cuts[i-1].EndSec {
			t.Errorf("cut[%d].StartSec = %v, want %v (must not overlap previous cut)", i, recipe.Cuts[i].StartSec, recipe.Cuts[i-1].EndSec)
		}
	}
	if got := recipe.Cuts[len(recipe.Cuts)-1].EndSec; got != 47 {
		t.Errorf("final EndSec = %v, want 47 (zero drift against the song timeline)", got)
	}
}

// TestSceneSplitFilterKeyframeScenesDiffusesRoundingError verifies the non-chain (keyframe)
// path also re-bases cuts onto the concatenated timeline and carries rounding error forward:
// a 9s cut splits to 8+4=12s (+3s), so the following 10s cut's target shrinks to 7s.
func TestSceneSplitFilterKeyframeScenesDiffusesRoundingError(t *testing.T) {
	recipe := &video.Recipe{
		ProjectTitle: "test",
		MusicRecipe:  video.MusicRecipe{Title: "test"},
		Cuts: []video.Cut{
			{CutIndex: 1, AudioSync: video.AudioSync{StartSec: 28, DurationSec: 9}},
			{CutIndex: 2, AudioSync: video.AudioSync{StartSec: 37, DurationSec: 10}},
		},
	}

	err := (SceneSplitFilter{}).Execute(context.Background(), &Context{
		State: State{
			Task:        &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate},
			VideoRecipe: recipe,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	wantDurations := []float64{8, 4, 8}
	wantStarts := []float64{28, 36, 40}
	if len(recipe.Cuts) != len(wantDurations) {
		t.Fatalf("cuts = %d, want %d", len(recipe.Cuts), len(wantDurations))
	}
	for i := range recipe.Cuts {
		if recipe.Cuts[i].DurationSec != wantDurations[i] {
			t.Errorf("cut[%d].DurationSec = %v, want %v", i, recipe.Cuts[i].DurationSec, wantDurations[i])
		}
		if recipe.Cuts[i].StartSec != wantStarts[i] {
			t.Errorf("cut[%d].StartSec = %v, want %v", i, recipe.Cuts[i].StartSec, wantStarts[i])
		}
	}
}

// TestSceneSplitAndExpandKeepVideoTimelineAlignedToSong pipes scene_split's planned chain
// blocks through expandCutsToSupportedDurations (the video_gen entry point) and verifies the
// final generated-cut durations sum to the song's musical length with no overlap, across a
// real section boundary.
func TestSceneSplitAndExpandKeepVideoTimelineAlignedToSong(t *testing.T) {
	recipe := &video.Recipe{
		ProjectTitle: "test",
		MusicRecipe:  video.MusicRecipe{Title: "test"},
		Cuts: []video.Cut{
			{CutIndex: 1, SectionIndex: 1, AudioSync: video.AudioSync{StartSec: 0, DurationSec: 8}},
			{CutIndex: 2, SectionIndex: 1, AudioSync: video.AudioSync{StartSec: 8, DurationSec: 9}},
			{CutIndex: 3, SectionIndex: 2, AudioSync: video.AudioSync{StartSec: 17, DurationSec: 11}},
		},
	}

	err := (SceneSplitFilter{UsePreviousVideo: true}).Execute(context.Background(), &Context{
		State: State{
			Task:        &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate},
			VideoRecipe: recipe,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := veo.ExpandCutsToSupportedDurations(recipe.Cuts, true, nil, false)

	total := 0.0
	for i := range got {
		total += got[i].DurationSec
		if got[i].DurationSec != veo.VideoExtensionDurationSec && got[i].DurationSec != 4 && got[i].DurationSec != 6 && got[i].DurationSec != 8 {
			t.Errorf("cut[%d].DurationSec = %v, want a Veo-supported value (4/6/8 base or 7 extension)", i, got[i].DurationSec)
		}
		if i > 0 && got[i].StartSec != got[i-1].EndSec {
			t.Errorf("cut[%d].StartSec = %v, want %v (contiguous timeline)", i, got[i].StartSec, got[i-1].EndSec)
		}
	}
	if total != 28 {
		t.Errorf("total video duration = %v, want exactly the 28s musical length", total)
	}
	// The section-2 cut must be flagged as a section start so video_gen skips last-frame
	// inheritance across the musical boundary.
	foundSectionStart := false
	for i := range got {
		if got[i].SectionIndex == 2 {
			foundSectionStart = got[i].IsSectionStart
			break
		}
	}
	if !foundSectionStart {
		t.Error("first cut of section 2 is not marked IsSectionStart")
	}
}

// TestSceneSplitFilterIsIdempotent pins the property the draft flow depends on: a recipe that
// already went through scene splitting must survive a second pass unchanged. Both
// mv_from_keyframe_video_recipe (RecipeLoad -> SceneSplit) and the draft flow re-run the filter
// over an already-split recipe, so a non-idempotent pass would silently re-plan the cuts the user
// just reviewed.
func TestSceneSplitFilterIsIdempotent(t *testing.T) {
	for _, tc := range []struct {
		name             string
		usePreviousVideo bool
	}{
		{"keyframe scenes", false},
		{"video-to-video chains", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recipe := &video.Recipe{
				ProjectTitle: "test",
				MusicRecipe:  video.MusicRecipe{Title: "test"},
				Cuts: []video.Cut{
					{CutIndex: 1, SectionIndex: 1, VisualAnchor: "rooftop at dawn", Dialogue: "a\nb\nc", AudioSync: video.AudioSync{StartSec: 0, DurationSec: 30, AudioCue: "0:00 to 0:30"}},
					{CutIndex: 2, SectionIndex: 2, VisualAnchor: "neon corridor", Dialogue: "d\ne", AudioSync: video.AudioSync{StartSec: 30, DurationSec: 19, AudioCue: "0:30 to 0:49"}},
				},
			}

			filter := SceneSplitFilter{UsePreviousVideo: tc.usePreviousVideo}
			newContext := func() *Context {
				return &Context{State: State{
					Task:        &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeCreate},
					VideoRecipe: recipe,
				}}
			}

			if err := filter.Execute(context.Background(), newContext()); err != nil {
				t.Fatalf("first Execute() error = %v", err)
			}
			first := append([]video.Cut(nil), recipe.Cuts...)

			if err := filter.Execute(context.Background(), newContext()); err != nil {
				t.Fatalf("second Execute() error = %v", err)
			}
			second := recipe.Cuts

			if len(second) != len(first) {
				t.Fatalf("second pass produced %d cuts, want %d (same as first pass)", len(second), len(first))
			}
			for i := range first {
				a, b := first[i], second[i]
				if a.DurationSec != b.DurationSec || a.StartSec != b.StartSec || a.EndSec != b.EndSec {
					t.Errorf("cut[%d] timing changed: first %v..%v (%vs), second %v..%v (%vs)",
						i, a.StartSec, a.EndSec, a.DurationSec, b.StartSec, b.EndSec, b.DurationSec)
				}
				if a.IsChainStart != b.IsChainStart {
					t.Errorf("cut[%d].IsChainStart changed: %v -> %v", i, a.IsChainStart, b.IsChainStart)
				}
				if a.IsSectionStart != b.IsSectionStart {
					t.Errorf("cut[%d].IsSectionStart changed: %v -> %v", i, a.IsSectionStart, b.IsSectionStart)
				}
				if a.VisualAnchor != b.VisualAnchor {
					t.Errorf("cut[%d].VisualAnchor changed:\n first  = %q\n second = %q", i, a.VisualAnchor, b.VisualAnchor)
				}
				if a.AudioCue != b.AudioCue {
					t.Errorf("cut[%d].AudioCue changed:\n first  = %q\n second = %q", i, a.AudioCue, b.AudioCue)
				}
				if a.Dialogue != b.Dialogue {
					t.Errorf("cut[%d].Dialogue changed: %q -> %q", i, a.Dialogue, b.Dialogue)
				}
			}
		})
	}
}

// TestSceneSplitFilterKeepsKeyframesOnOneToOneReallocation pins the half of the keyframe-reuse
// fix that lives here: a cut that re-allocates to a single block keeps the image already baked
// for it, so CutKeyframeFilter can skip regeneration. Without this, "generate a video from the
// saved keyframes" re-bakes every image first.
func TestSceneSplitFilterKeepsKeyframesOnOneToOneReallocation(t *testing.T) {
	for _, tc := range []struct {
		name             string
		usePreviousVideo bool
	}{
		{"keyframe scenes", false},
		{"video-to-video chains", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recipe := &video.Recipe{
				ProjectTitle: "test",
				MusicRecipe:  video.MusicRecipe{Title: "test"},
				Cuts: []video.Cut{{
					CutIndex:       1,
					VisualAnchor:   "rooftop",
					AudioSync:      video.AudioSync{StartSec: 0, DurationSec: 8, AudioCue: "0:00 to 0:08"},
					KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://bucket/jobs/job-1/images/keyframe_001.png"},
					ChainControl:   video.ChainControl{IsChainStart: true},
				}},
			}

			err := (SceneSplitFilter{UsePreviousVideo: tc.usePreviousVideo}).Execute(context.Background(), &Context{
				State: State{
					Task:        &domain.Task{JobID: "mv-1", Command: domain.CommandMVFromKeyframeVideoRecipe},
					VideoRecipe: recipe,
				},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if len(recipe.Cuts) != 1 {
				t.Fatalf("cuts = %d, want 1 (an 8s cut re-allocates to a single block)", len(recipe.Cuts))
			}
			if got := recipe.Cuts[0].KeyframeReference; got != "gs://bucket/jobs/job-1/images/keyframe_001.png" {
				t.Errorf("KeyframeReference = %q, want the existing image to be kept", got)
			}
			// 生成状態は落とす（動画は作り直す）が、絵は使い回す、が意図。
			if recipe.Cuts[0].Status != video.CutStatusPending || recipe.Cuts[0].VideoID != "" {
				t.Errorf("video generation state was not reset: status=%q videoID=%q", recipe.Cuts[0].Status, recipe.Cuts[0].VideoID)
			}
		})
	}
}

// TestSceneSplitFilterDropsKeyframeWhenCutIsResplit pins the conservative half: once a cut is
// divided, one image can no longer stand for several cuts that each get their own scene beat.
func TestSceneSplitFilterDropsKeyframeWhenCutIsResplit(t *testing.T) {
	recipe := &video.Recipe{
		ProjectTitle: "test",
		MusicRecipe:  video.MusicRecipe{Title: "test"},
		Cuts: []video.Cut{{
			CutIndex:       1,
			VisualAnchor:   "rooftop",
			AudioSync:      video.AudioSync{StartSec: 0, DurationSec: 20, AudioCue: "0:00 to 0:20"},
			KeyframeResult: video.KeyframeResult{KeyframeReference: "gs://bucket/jobs/job-1/images/keyframe_001.png"},
		}},
	}

	err := (SceneSplitFilter{}).Execute(context.Background(), &Context{
		State: State{
			Task:        &domain.Task{JobID: "mv-1", Command: domain.CommandMVFromKeyframeVideoRecipe},
			VideoRecipe: recipe,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(recipe.Cuts) < 2 {
		t.Fatalf("cuts = %d, want the 20s cut to be split", len(recipe.Cuts))
	}
	for i, cut := range recipe.Cuts {
		if cut.KeyframeReference != "" {
			t.Errorf("cut[%d].KeyframeReference = %q, want cleared for a re-split cut", i, cut.KeyframeReference)
		}
	}
}

func TestAllocateChainDurations(t *testing.T) {
	full := veo.ChainDurations(veo.ImageToVideoDurationsSec())
	reference := veo.ChainDurations(veo.ReferenceToVideoDurationsSec())
	cases := []struct {
		name       string
		target     float64
		candidates []float64
		want       []float64
	}{
		{"rounds down when nearest", 9, full, []float64{8}},
		{"exact single chain", 11, full, []float64{11}},
		{"prefers exact sum over fewer blocks", 16, full, []float64{8, 8}},
		{"long cut allocates exactly", 50, full, []float64{6, 22, 22}},
		{"tiny target clamps to smallest", 2, full, []float64{4}},
		{"reference cuts only use 8s bases", 23, reference, []float64{8, 15}},
		{"just above one chain", 24, full, []float64{4, 20}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := allocateChainDurations(tc.target, tc.candidates)
			if len(got) != len(tc.want) {
				t.Fatalf("allocateChainDurations(%v) = %v, want %v", tc.target, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("allocateChainDurations(%v) = %v, want %v", tc.target, got, tc.want)
				}
			}
		})
	}
}
