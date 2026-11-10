package ports

import "context"

// VideoProcessor is the boundary for local video processing (frame extraction, joining) of generated videos.
// ap-mv is at heart "a service that calls the Veo API to generate video", and only this boundary
// exceptionally handles video binaries (to keep visual continuity on continuation-chain resets and
// to make several chains into one at the end).
type VideoProcessor interface {
	// ExtractLastFrame extracts the last frame of videoURI as an image and uploads it to destURI.
	// On success it returns the URI actually uploaded (usually the same as destURI).
	ExtractLastFrame(ctx context.Context, videoURI, destURI string) (string, error)
	// ConcatHardCut joins videoURIs in the order given with no transitions (hard cuts) and uploads
	// the result to destURI. With a single videoURI it copies it to destURI as-is without joining.
	// On success it returns the URI actually uploaded.
	ConcatHardCut(ctx context.Context, videoURIs []string, destURI string) (string, error)
	// ColorMatchSaturation corrects the saturation of the whole videoURI to match the average
	// saturation of referenceImageURI (the character reference art) and uploads it to destURI. Veo's
	// video_extension reuses the previous output as conditioning input, so saturation drifts and
	// accumulates with every continuation (confirmed in operation). Pulling each generation's output
	// back to the character reference every time keeps the drift from compounding into the next
	// generation. On success it returns the URI actually uploaded.
	ColorMatchSaturation(ctx context.Context, videoURI, referenceImageURI, destURI string) (string, error)
	// Probe analyses videoURI and returns its actual duration and whether it has an audio track.
	// It is used to check that the join came out as the script says without playing the output.
	Probe(ctx context.Context, videoURI string) (VideoStats, error)
}

// VideoStats are values measured from a generated video.
//
// The script holds each cut's duration, but nothing guarantees the actual length of the clips Veo
// returns matches it, and nobody checked until the join was measured.
type VideoStats struct {
	// DurationSeconds is the actual length of the whole video.
	DurationSeconds float64
	// HasAudio is whether it has an audio track. Even with -c:a given to ffmpeg, an input without
	// audio produces silent output and succeeds.
	HasAudio bool
}
