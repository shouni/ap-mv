package domain

import "time"

// NotificationRequest contains task metadata used by completion/error notifiers.
type NotificationRequest struct {
	JobID string
	// HistoryJobID is the job ID whose History Detail page actually holds the result
	// (equal to JobID, except for regenerate tasks which write back to the original job).
	HistoryJobID string
	Command      string
	Title        string
	SourceURL    string
	RecipeURL    string
	AudioURL     string
	CharacterID  string
	VisualMode   string
	TextModel    string
	ImageModel   string
	OutputURI    string
	CutCount     int
	CreatedAt    time.Time
}
