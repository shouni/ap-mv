package domain

import (
	"strconv"
	"strings"
)

// JobStage is how far a job has progressed through generation.
//
// There used to be only one boolean, "has every cut's video been generated", and the display had
// two values, generated or keyframes. So "a job that has not rendered a single keyframe" and "a
// job with just one video left" looked the same, and neither progress nor remaining work could be
// read. When advancing a little at a time per cut, the billing went on while that stayed invisible.
type JobStage string

const (
	// StageScript means the cut list is settled and not a single keyframe has been rendered yet.
	StageScript JobStage = "script"
	// StageKeyframes means keyframes are being rendered (one or more, not all).
	StageKeyframes JobStage = "keyframes"
	// StageKeyframesDone means every cut has its keyframe and not a single video has been made yet.
	StageKeyframesDone JobStage = "keyframes_done"
	// StageVideos means videos are being generated (one or more, not all).
	StageVideos JobStage = "videos"
	// StageCompleted means video generation has finished for every cut.
	StageCompleted JobStage = "completed"
)

// JobProgress is a job's progress stage and the counts it is based on.
//
// The page picks the badge colour from Stage and shows what remains as a fraction (3/12 and so
// on). A job whose total is 0 (a recipe with no cuts) has Stage StageScript.
// The firestore tags cannot be omitted. It is embedded in JobStatus and saved to the document, so
// without them the stored field names become the Go identifiers (TotalCuts) and disagree with the
// progress.stage the listing filter looks at.
type JobProgress struct {
	Stage JobStage `json:"stage" firestore:"stage"`
	// TotalCuts is the recipe's total number of cuts.
	TotalCuts int `json:"total_cuts" firestore:"total_cuts"`
	// KeyframeCuts is the number of cuts with a keyframe image.
	KeyframeCuts int `json:"keyframe_cuts" firestore:"keyframe_cuts"`
	// VideoCuts is the number of cuts whose video generation has finished.
	VideoCuts int `json:"video_cuts" firestore:"video_cuts"`
}

// IsCompleted reports whether video generation has finished for every cut.
// It means the same as the old VideoHistory.Generated, and existing branches can be replaced with it.
func (p JobProgress) IsCompleted() bool {
	return p.Stage == StageCompleted
}

// Label returns the short progress text shown on the page (e.g. "keyframes 3/12", "completed").
// Intermediate stages get a fraction so the remainder can be read, not just the stage.
func (p JobProgress) Label() string {
	switch p.Stage {
	case StageKeyframes:
		return "keyframes " + strconv.Itoa(p.KeyframeCuts) + "/" + strconv.Itoa(p.TotalCuts)
	case StageVideos:
		return "videos " + strconv.Itoa(p.VideoCuts) + "/" + strconv.Itoa(p.TotalCuts)
	case StageKeyframesDone:
		return "keyframes done"
	default:
		return string(p.Stage)
	}
}

// NewJobProgress counts the progress stage from the recipe's cuts.
//
// Keyframe presence is KeyframeReference, video completion is Cut.IsGenerated().
// If there is even one video a keyframe always exists, so the stages are judged from the top down.
func NewJobProgress(cuts []VideoCut) JobProgress {
	progress := JobProgress{Stage: StageScript, TotalCuts: len(cuts)}
	for _, cut := range cuts {
		if strings.TrimSpace(cut.KeyframeReference) != "" {
			progress.KeyframeCuts++
		}
		if cut.IsGenerated() {
			progress.VideoCuts++
		}
	}

	switch {
	case progress.TotalCuts == 0:
		// A recipe with no cuts is treated as script only (so 0/0 does not read as "completed").
		progress.Stage = StageScript
	case progress.VideoCuts == progress.TotalCuts:
		progress.Stage = StageCompleted
	case progress.VideoCuts > 0:
		progress.Stage = StageVideos
	case progress.KeyframeCuts == progress.TotalCuts:
		progress.Stage = StageKeyframesDone
	case progress.KeyframeCuts > 0:
		progress.Stage = StageKeyframes
	default:
		progress.Stage = StageScript
	}
	return progress
}

// TotalDurationSecOfCuts returns the total duration of every cut. Unlike GeneratedSecondsOfCuts
// it does not care whether they are generated, so it works even for a job that has not generated a single cut.
func TotalDurationSecOfCuts(cuts []VideoCut) float64 {
	total := 0.0
	for _, cut := range cuts {
		total += cut.DurationSec
	}
	return total
}
