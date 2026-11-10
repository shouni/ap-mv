package domain

import (
	"strings"

	"github.com/shouni/gcp-kit/jobstatus"
)

// VideoHistory is the metadata shown in the generated MV history list.
type VideoHistory struct {
	JobID      string `json:"job_id"`
	Title      string `json:"title"`
	Mood       string `json:"mood,omitempty"`
	Tempo      int    `json:"tempo,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	VisualMode string `json:"visual_mode,omitempty"`
	CutCount   int    `json:"cut_count,omitempty"`
	StorageURI string `json:"storage_uri,omitempty"`
	// SignedURL is the signed URL for StorageURI. **It goes into JSON responses only.**
	// The page follows a same-origin path (MetadataWebURL) and the handler 302s to GCS.
	// Embedding the signature in the page would let that URL be used outside authentication until
	// it expires, and after expiry nothing recovers until the page is reopened.
	SignedURL string `json:"signed_url,omitempty"`
	// MetadataWebURL is the same-origin path the page follows. It is not put in JSON.
	MetadataWebURL string `json:"-"`
	// Generated is whether video generation has finished for every cut. It means the same as
	// Progress.IsCompleted() and is kept for compatibility with existing API responses. Pages should use Progress.
	Generated bool `json:"generated,omitempty"`
	// Progress is the progress stage (script only, keyframes partway, videos partway, complete)
	// and the cut counts it is based on. Generated alone could not tell "not a single one rendered
	// yet" from "one more to go".
	Progress JobProgress `json:"progress"`
	// State is the job's own state (queued/running/succeeded/failed).
	//
	// Both this and Progress exist because they say different things. Progress is how far the cuts
	// have been rendered; State is whether processing is alive. A failed job stops at
	// "keyframes 3/12", so without State it cannot be told from a job in progress.
	// It is empty for jobs without a record (from when the listing scanned artifacts).
	State JobState `json:"state,omitempty"`
	// Error is the reason when State is failed.
	Error          string `json:"error,omitempty"`
	KeyframeZipURI string `json:"keyframe_zip_uri,omitempty"`
	// FinalVideoURL is the GCS URI of the finished video joining the continuation chain into one
	// with hard cuts (video.Recipe.FinalVideoURL, set by chain_finalize.go).
	FinalVideoURL string `json:"final_video_url,omitempty"`
	// FinalVideoSignedURL is the signed URL for FinalVideoURL. Like SignedURL, **it goes into JSON
	// responses only** (the page uses a 302 via FinalVideoWebURL).
	FinalVideoSignedURL string `json:"final_video_signed_url,omitempty"`
	// FinalVideoWebURL is the same-origin path the page follows. It is not put in JSON.
	FinalVideoWebURL string `json:"-"`
	// AspectRatio is the aspect ratio used for this job's keyframe and video generation
	// (e.g. "16:9", "9:16"). It is decided once when keyframes are made, and video generation and
	// cut regeneration on an existing job inherit it as-is (the aspect ratio is never chosen again).
	AspectRatio string `json:"aspect_ratio,omitempty"`
	// GeneratedSeconds is the total duration of generated cuts (= an estimate of Veo's billable seconds).
	// It is settled when the recipe is read, so caching it does not go stale.
	GeneratedSeconds float64 `json:"generated_seconds,omitempty"`
	// Cost is the estimated cost, GeneratedSeconds times the price. The price is resolved from the
	// config at display time, so the handler fills it rather than the repository (domain.ApplyVeoCostEstimate).
	Cost VideoCostEstimate `json:"cost,omitzero"`
}

// VideoHistoryCut is a display-ready cut entry for a generated MV history detail.
type VideoHistoryCut struct {
	CutIndex          int     `json:"cut_index"`
	DurationSec       float64 `json:"duration_sec,omitempty"`
	AudioCue          string  `json:"audio_cue,omitempty"`
	VisualAnchor      string  `json:"visual_anchor,omitempty"`
	CharacterID       string  `json:"character_id,omitempty"`
	Dialogue          string  `json:"dialogue,omitempty"`
	KeyframeReference string  `json:"keyframe_reference,omitempty"`
	// KeyframeURL is the signed URL for KeyframeReference. **It goes into JSON responses only.**
	KeyframeURL string `json:"keyframe_url,omitempty"`
	// KeyframeWebURL is the same-origin path the page follows. It is not put in JSON.
	KeyframeWebURL string `json:"-"`
	VideoURL       string `json:"video_url,omitempty"`
	// VideoSignedURL is the signed URL for VideoURL (a gs:// URI). **It goes into JSON responses only.**
	VideoSignedURL string `json:"video_signed_url,omitempty"`
	// VideoWebURL is the same-origin path the page follows. It is not put in JSON.
	VideoWebURL string  `json:"-"`
	Status      string  `json:"status,omitempty"`
	StartSec    float64 `json:"start_sec,omitempty"`
	EndSec      float64 `json:"end_sec,omitempty"`
	// EstimatedCostUSD is the estimate DurationSec × the Veo price. It is 0 for ungenerated cuts.
	// The price is resolved from the config at display time, so the handler fills it rather than
	// the repository (domain.ApplyVeoCostEstimate).
	EstimatedCostUSD float64 `json:"estimated_cost_usd,omitempty"`
	// GenerationCount is the number of Veo generations that actually succeeded for this cut.
	// 2 or more means it was re-rendered. It is filled only when there is a usage record (veo_usage.json).
	GenerationCount int `json:"generation_count,omitempty"`
}

// IsGenerated reports whether this cut can be treated as having its video generated.
//
// It judges on different inputs from the orchestrator's VideoResult.IsGenerated, which holds the
// generation result itself: display cuts have no video_id, so only status and video_url decide.
// In video_music_meta.json both are filled together, so the results agree in practice.
func (c VideoHistoryCut) IsGenerated() bool {
	return c.Status == CutStatusGenerated || strings.TrimSpace(c.VideoURL) != ""
}

// AudioCueParts is the display-ready breakdown of an AudioCue string into its instrumental/
// section description, the sung lyric line, and (when the cut was split into scene beats)
// scene-beat guidance.
type AudioCueParts struct {
	Description string
	Vocal       string
	SceneBeat   string
}

// AudioCueParts splits AudioCue into its components for a more scannable history detail
// display. AudioCue is free-form text: ScriptingFilter's LLM output describes the section
// then quotes the sung line (phrasing varies — "Vocal:", "Vocal begins:", "Vocal continues:",
// ...), and SceneSplitFilter (internal/pipeline/step/scene_split.go's sceneAudioCue) appends
// " / scene beat N/M: <guidance>" when a cut is split into sub-cuts. Rather than matching one
// exact "Vocal:" label, this looks for "Vocal" followed by a quoted line, so any phrasing works.
// Parsing is best-effort: text that doesn't match either marker stays in Description, so
// nothing is silently dropped.
//
// Note: the quoted lyric is usually identical to VideoHistoryCut.Dialogue (the structured
// per-cut lyric line assigned by applyLyricsToVideoRecipeCuts) — callers that already render
// Dialogue should skip Vocal to avoid showing the same line twice.
func (c VideoHistoryCut) AudioCueParts() AudioCueParts {
	text := strings.TrimSpace(c.AudioCue)
	var parts AudioCueParts
	if before, after, ok := strings.Cut(text, " / scene beat "); ok {
		parts.SceneBeat = "scene beat " + strings.TrimSpace(after)
		text = strings.TrimSpace(before)
	}
	if vocalIdx := strings.Index(text, "Vocal"); vocalIdx >= 0 {
		if quote, quoteStart := firstQuoteAt(text, vocalIdx); quoteStart >= 0 {
			if closeOffset := strings.IndexByte(text[quoteStart+1:], quote); closeOffset >= 0 {
				parts.Description = strings.TrimSpace(text[:vocalIdx])
				parts.Vocal = text[quoteStart+1 : quoteStart+1+closeOffset]
				return parts
			}
		}
	}
	parts.Description = text
	return parts
}

// firstQuoteAt returns the byte and index of the first ' or " in text at or after start, or
// (0, -1) if neither appears. Indexing at an ASCII quote byte is always a valid UTF-8 slice
// boundary even when the surrounding text (e.g. lyrics) is non-ASCII.
func firstQuoteAt(text string, start int) (byte, int) {
	idx := strings.IndexAny(text[start:], "'\"")
	if idx < 0 {
		return 0, -1
	}
	pos := start + idx
	return text[pos], pos
}

// VideoHistorySection is a display-ready song section entry for a generated MV history detail.
// SectionIndex is the position in the recipe's sections array; section names (e.g. "Chorus")
// can repeat within a song, so selections reference the index rather than the name.
type VideoHistorySection struct {
	SectionIndex int    `json:"section_index"`
	Name         string `json:"name"`
	StartSeconds int    `json:"start_seconds"`
	EndSeconds   int    `json:"end_seconds"`
	// Generated is true when every cut whose StartSec falls within this section already has
	// status=generated, meaning a "generate video from history" request for this section would
	// re-trigger real Veo generation (SectionSelectFilter resets already-generated cuts to
	// pending) rather than being skipped like a redundant full-recipe request would be.
	Generated bool `json:"generated,omitempty"`
}

// VideoHistoryDetail contains generated MV metadata and display-ready cuts.
type VideoHistoryDetail struct {
	VideoHistory
	Cuts     []VideoHistoryCut     `json:"cuts,omitempty"`
	Sections []VideoHistorySection `json:"sections,omitempty"`
}

// VideoHistorySectionGroup pairs a song section with the cuts whose StartSec falls inside its
// time range, for section-grouped display on the history detail page (SceneSplitFilter can
// expand a single section into many cuts, so a flat cut list is hard to scan).
type VideoHistorySectionGroup struct {
	// Section is the zero value for the trailing "unassigned" group (SectionIndex 0, empty
	// Name), used for cuts whose StartSec doesn't fall within any known section.
	Section VideoHistorySection
	Cuts    []VideoHistoryCut
}

// SectionGroups buckets Cuts by which Sections time range their StartSec falls into, preserving
// section order. Returns nil when there are no sections (e.g. jobs predating StartSec/Sections,
// or short-video jobs), so callers should fall back to rendering Cuts as a flat list.
func (d VideoHistoryDetail) SectionGroups() []VideoHistorySectionGroup {
	if len(d.Sections) == 0 {
		return nil
	}
	groups := make([]VideoHistorySectionGroup, len(d.Sections))
	for i, sec := range d.Sections {
		groups[i].Section = sec
	}
	var unassigned []VideoHistoryCut
	for _, cut := range d.Cuts {
		idx := sectionIndexForCutStart(d.Sections, cut.StartSec)
		if idx < 0 {
			unassigned = append(unassigned, cut)
			continue
		}
		groups[idx].Cuts = append(groups[idx].Cuts, cut)
	}
	if len(unassigned) > 0 {
		groups = append(groups, VideoHistorySectionGroup{Cuts: unassigned})
	}
	return groups
}

// sectionIndexForCutStart returns the index of the section whose StartSeconds is the largest
// value still <= startSec, mirroring internal/worker/filter's sectionIndexForStartSec so a cut
// lands in the right section even with the small StartSec/EndSeconds rounding gaps duration
// normalization can introduce. Returns -1 when startSec is before every section's start.
func sectionIndexForCutStart(sections []VideoHistorySection, startSec float64) int {
	bestIndex := -1
	bestStart := -1
	for i, s := range sections {
		if float64(s.StartSeconds) <= startSec && s.StartSeconds >= bestStart {
			bestIndex = i
			bestStart = s.StartSeconds
		}
	}
	return bestIndex
}

// PageMeta contains pagination metadata for history list views.
type PageMeta = jobstatus.PageMeta

// VideoHistoryPage contains a page of generated MV history items.
type VideoHistoryPage struct {
	Items []VideoHistory `json:"items"`
	PageMeta
}
