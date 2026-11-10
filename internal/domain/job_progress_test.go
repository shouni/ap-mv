package domain

import (
	"testing"

	"github.com/shouni/go-veo-orchestrator/video"
)

func cutWithKeyframe(ref string) VideoCut {
	cut := VideoCut{
		KeyframeReference: ref}
	return cut
}

func generatedCut(ref string) VideoCut {
	cut := cutWithKeyframe(ref)
	cut.Status = video.CutStatusGenerated
	cut.VideoID = "gs://bucket/v.mp4"
	cut.VideoURL = "gs://bucket/v.mp4"
	return cut
}

// TestNewJobProgressStages pins the distinction the old boolean could not express: a job that
// has baked nothing and a job one video short of done used to render identically.
func TestNewJobProgressStages(t *testing.T) {
	tests := map[string]struct {
		cuts      []VideoCut
		wantStage JobStage
		wantLabel string
	}{
		"no cuts is script only": {
			cuts:      nil,
			wantStage: StageScript,
			wantLabel: "script",
		},
		"zero keyframes is script only": {
			cuts:      []VideoCut{{}, {}},
			wantStage: StageScript,
			wantLabel: "script",
		},
		"keyframes partway": {
			cuts:      []VideoCut{cutWithKeyframe("gs://k1.png"), {}, {}},
			wantStage: StageKeyframes,
			wantLabel: "keyframes 1/3",
		},
		"keyframes done, zero videos": {
			cuts:      []VideoCut{cutWithKeyframe("gs://k1.png"), cutWithKeyframe("gs://k2.png")},
			wantStage: StageKeyframesDone,
			wantLabel: "keyframes done",
		},
		"one video left": {
			cuts:      []VideoCut{generatedCut("gs://k1.png"), generatedCut("gs://k2.png"), cutWithKeyframe("gs://k3.png")},
			wantStage: StageVideos,
			wantLabel: "videos 2/3",
		},
		"completed": {
			cuts:      []VideoCut{generatedCut("gs://k1.png"), generatedCut("gs://k2.png")},
			wantStage: StageCompleted,
			wantLabel: "completed",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := NewJobProgress(tt.cuts)
			if got.Stage != tt.wantStage {
				t.Errorf("Stage = %q, want %q", got.Stage, tt.wantStage)
			}
			if label := got.Label(); label != tt.wantLabel {
				t.Errorf("Label() = %q, want %q", label, tt.wantLabel)
			}
		})
	}
}
