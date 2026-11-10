// Package repository persists generation history, formats its listings, and reads and writes job status.
package repository

import (
	"github.com/shouni/go-utils/jobid"
	"github.com/shouni/go-utils/jst"
)

// formatHistoryCreatedAt turns the creation time embedded in jobID into a string for the history display.
// For an ID the time cannot be taken from, it returns an empty string and the listing shows no date.
//
// Minting is UTC and conversion to JST happens only just before display. It does not depend on
// the runtime's time zone setting (the container's TZ and the like).
func formatHistoryCreatedAt(jobID string) string {
	createdAt, err := jobid.CreatedAt(jobID)
	if err != nil {
		return ""
	}
	return jst.FormatDisplay(createdAt)
}
