package domain

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
	SignedURL  string `json:"signed_url,omitempty"`
	Generated  bool   `json:"generated,omitempty"`
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
