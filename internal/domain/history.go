package domain

import "strings"

// VideoHistory is the metadata shown in the generated MV history list.
type VideoHistory struct {
	JobID          string `json:"job_id"`
	Title          string `json:"title"`
	Mood           string `json:"mood,omitempty"`
	Tempo          int    `json:"tempo,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	VisualMode     string `json:"visual_mode,omitempty"`
	CutCount       int    `json:"cut_count,omitempty"`
	StorageURI     string `json:"storage_uri,omitempty"`
	SignedURL      string `json:"signed_url,omitempty"`
	Generated      bool   `json:"generated,omitempty"`
	KeyframeZipURI string `json:"keyframe_zip_uri,omitempty"`
	// FinalVideoURL は、継続チェーンをハードカットで1本に結合した完成動画のGCS URIです
	// (orchestrator.VideoRecipe.FinalVideoURL、chain_finalize.goが設定)。
	FinalVideoURL string `json:"final_video_url,omitempty"`
	// FinalVideoSignedURL は FinalVideoURL をブラウザ再生用に署名したURLです。期限付きのため
	// キャッシュされず、表示ごとに再生成されます（SignedURLと同じ扱い）。
	FinalVideoSignedURL string `json:"final_video_signed_url,omitempty"`
	// AspectRatio は、このジョブのキーフレーム・動画生成に使われたアスペクト比です
	// （例: "16:9", "9:16"）。キーフレーム作成時に一度だけ決まり、既存ジョブに対する
	// 動画生成・カット再生成はこの値をそのまま引き継ぎます（アスペクト比を都度選び直すことはない）。
	AspectRatio string `json:"aspect_ratio,omitempty"`
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
	KeyframeURL       string  `json:"keyframe_url,omitempty"`
	VideoURL          string  `json:"video_url,omitempty"`
	// VideoSignedURL は VideoURL（gs:// URI）をブラウザ再生用に署名した URL です。
	// 署名 URL は期限付きのため cache されず、表示ごとに再生成されます。
	VideoSignedURL string  `json:"video_signed_url,omitempty"`
	Status         string  `json:"status,omitempty"`
	StartSec       float64 `json:"start_sec,omitempty"`
	EndSec         float64 `json:"end_sec,omitempty"`
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
// ...), and SceneSplitFilter (internal/worker/filter/scene_split.go's sceneAudioCue) appends
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
type PageMeta struct {
	Page       int  `json:"page"`
	PerPage    int  `json:"per_page"`
	Total      int  `json:"total"`
	TotalPages int  `json:"total_pages"`
	HasPrev    bool `json:"has_prev"`
	HasNext    bool `json:"has_next"`
	PrevPage   int  `json:"prev_page"`
	NextPage   int  `json:"next_page"`
	From       int  `json:"from"`
	To         int  `json:"to"`
}

// VideoHistoryPage contains a page of generated MV history items.
type VideoHistoryPage struct {
	Items []VideoHistory `json:"items"`
	PageMeta
}
