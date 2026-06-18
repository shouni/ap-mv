package domain

import "time"

const NotAvailable = "N/A"

// NotificationRequest contains task metadata used by completion/error notifiers.
type NotificationRequest struct {
	JobID       string
	Command     string
	Title       string
	SourceURL   string
	RecipeURL   string
	AudioURL    string
	CharacterID string
	VisualMode  string
	TextModel   string
	ImageModel  string
	OutputURI   string
	CutCount    int
	CreatedAt   time.Time
}
