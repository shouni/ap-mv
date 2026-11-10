package domain

import (
	"fmt"
	"path"
	"strings"
)

// VideoCutsToHistoryCuts maps the recipe's cuts onto the VideoHistoryCut list that ASS subtitle
// generation takes. It is a conversion that carries only the fields ASS generation (GenerateASS)
// uses, and since both repository (the keyframe ZIP) and worker/filter (the artifact ZIP) go
// through it, the subtitle timing rules come down to one implementation.
func VideoCutsToHistoryCuts(cuts []VideoCut) []VideoHistoryCut {
	result := make([]VideoHistoryCut, 0, len(cuts))
	for _, c := range cuts {
		result = append(result, VideoHistoryCut{
			CutIndex:    c.CutIndex,
			DurationSec: c.DurationSec,
			Dialogue:    c.Dialogue,
			StartSec:    c.StartSec,
			EndSec:      c.EndSec,
		})
	}
	return result
}

// BuildFFmpegInputsTxt builds inputs.txt for ffmpeg's concat demuxer from the cuts that have a
// keyframe. File names are the cut number + the reference's extension (.png when none), and must
// match the keyframe file names stored in the ZIP.
// The same format used to be written separately in two places, repository and worker/filter,
// so the definition is kept here, once.
func BuildFFmpegInputsTxt(cuts []VideoCut) string {
	var sb strings.Builder
	for _, cut := range cuts {
		ref := strings.TrimSpace(cut.KeyframeReference)
		if ref == "" {
			continue
		}
		ext := path.Ext(ref)
		if ext == "" {
			ext = ".png"
		}
		fmt.Fprintf(&sb, "file 'cut_%02d%s'\n", cut.CutIndex, ext)
		if cut.DurationSec > 0 {
			fmt.Fprintf(&sb, "duration %.3f\n", cut.DurationSec)
		}
		fmt.Fprintln(&sb)
	}
	return sb.String()
}
