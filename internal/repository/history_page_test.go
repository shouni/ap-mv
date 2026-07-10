package repository

import (
	"testing"
)

// TestSelectHistoryPageIDsSortsByCreatedAtDesc verifies that jobs are ordered by the creation
// timestamp embedded in the job ID (newest first), not by the job ID prefix.
func TestSelectHistoryPageIDsSortsByCreatedAtDesc(t *testing.T) {
	jobIDs := []string{
		"video-recipe-20260706-194856-aaa",
		"mv-20260711-010101-bbb",
		"short-20260710-155126-ccc",
		"no-timestamp-job",
	}

	selected, meta := selectHistoryPageIDs(jobIDs, 1, 10)

	want := []string{
		"mv-20260711-010101-bbb",
		"short-20260710-155126-ccc",
		"video-recipe-20260706-194856-aaa",
		"no-timestamp-job",
	}
	if len(selected) != len(want) {
		t.Fatalf("selected = %d ids, want %d", len(selected), len(want))
	}
	for i := range want {
		if selected[i] != want[i] {
			t.Fatalf("selected[%d] = %q, want %q (got order %v)", i, selected[i], want[i], selected)
		}
	}
	if meta.Total != 4 {
		t.Fatalf("meta.Total = %d, want 4", meta.Total)
	}
}

// TestHistoryCreatedAtRaw verifies timestamp extraction from job IDs.
func TestHistoryCreatedAtRaw(t *testing.T) {
	tests := []struct {
		jobID string
		want  string
	}{
		{jobID: "video-recipe-20260706-194856-efeeccfc3b0c", want: "20260706194856"},
		{jobID: "short-20260710-155126-3e9e7db8cad2", want: "20260710155126"},
		{jobID: "no-timestamp", want: ""},
	}
	for _, tt := range tests {
		if got := historyCreatedAtRaw(tt.jobID); got != tt.want {
			t.Errorf("historyCreatedAtRaw(%q) = %q, want %q", tt.jobID, got, tt.want)
		}
	}
}
