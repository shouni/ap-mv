package domain

import (
	"strings"
	"testing"
)

func TestGenerateASS_empty(t *testing.T) {
	if got := GenerateASS(nil); got != "" {
		t.Fatalf("expected empty string for nil cuts, got %q", got)
	}
	cuts := []VideoHistoryCut{{CutIndex: 1, StartSec: 0, DurationSec: 4}}
	if got := GenerateASS(cuts); got != "" {
		t.Fatalf("expected empty string when no dialogue, got %q", got)
	}
}

func TestGenerateASS_basic(t *testing.T) {
	cuts := []VideoHistoryCut{
		{CutIndex: 1, StartSec: 0, DurationSec: 4, Dialogue: "Hello world"},
		{CutIndex: 2, StartSec: 4, EndSec: 8, Dialogue: "Second line"},
		{CutIndex: 3, StartSec: 8, DurationSec: 4}, // no dialogue, skipped
	}
	got := GenerateASS(cuts)
	if !strings.Contains(got, "[Events]") {
		t.Fatal("missing [Events] section")
	}
	if !strings.Contains(got, "0:00:00.00,0:00:04.00") {
		t.Fatalf("wrong timing for first cut: %s", got)
	}
	if !strings.Contains(got, "0:00:04.00,0:00:08.00") {
		t.Fatalf("wrong timing for second cut: %s", got)
	}
	if !strings.Contains(got, "Hello world") || !strings.Contains(got, "Second line") {
		t.Fatalf("dialogue text missing: %s", got)
	}
	if strings.Contains(got, "CutIndex: 3") || strings.Count(got, "Dialogue:") != 2 {
		t.Fatal("empty-dialogue cut should be skipped")
	}
}

func TestGenerateASS_multilineDialogue(t *testing.T) {
	cuts := []VideoHistoryCut{
		{CutIndex: 1, StartSec: 0, DurationSec: 4, Dialogue: "line one\nline two"},
	}
	got := GenerateASS(cuts)
	if !strings.Contains(got, `line one\Nline two`) {
		t.Fatalf("newline should be converted to \\N, got: %s", got)
	}
}

func TestAssTime(t *testing.T) {
	cases := []struct {
		sec  float64
		want string
	}{
		{0, "0:00:00.00"},
		{1.5, "0:00:01.50"},
		{61.25, "0:01:01.25"},
		{3661.0, "1:01:01.00"},
	}
	for _, tc := range cases {
		if got := assTime(tc.sec); got != tc.want {
			t.Errorf("assTime(%v) = %q, want %q", tc.sec, got, tc.want)
		}
	}
}
