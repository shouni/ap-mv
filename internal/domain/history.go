package domain

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
	Status            string  `json:"status,omitempty"`
	StartSec          float64 `json:"start_sec,omitempty"`
	EndSec            float64 `json:"end_sec,omitempty"`
}

// VideoHistorySection is a display-ready song section entry for a generated MV history detail.
// SectionIndex is the position in the recipe's sections array; section names (e.g. "Chorus")
// can repeat within a song, so selections reference the index rather than the name.
type VideoHistorySection struct {
	SectionIndex int    `json:"section_index"`
	Name         string `json:"name"`
	StartSeconds int    `json:"start_seconds"`
	EndSeconds   int    `json:"end_seconds"`
}

// VideoHistoryDetail contains generated MV metadata and display-ready cuts.
type VideoHistoryDetail struct {
	VideoHistory
	Cuts     []VideoHistoryCut     `json:"cuts,omitempty"`
	Sections []VideoHistorySection `json:"sections,omitempty"`
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
