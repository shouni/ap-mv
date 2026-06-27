package domain

import (
	"strings"
	"testing"
)

func TestGenerateASS_empty(t *testing.T) {
	if got := GenerateASS(nil, ASSColors{}); got != "" {
		t.Fatalf("expected empty string for nil cuts, got %q", got)
	}
	cuts := []VideoHistoryCut{{CutIndex: 1, StartSec: 0, DurationSec: 4}}
	if got := GenerateASS(cuts, ASSColors{}); got != "" {
		t.Fatalf("expected empty string when no dialogue, got %q", got)
	}
}

func TestGenerateASS_basic(t *testing.T) {
	cuts := []VideoHistoryCut{
		{CutIndex: 1, StartSec: 0, DurationSec: 4, Dialogue: "Hello world"},
		{CutIndex: 2, StartSec: 4, EndSec: 8, Dialogue: "Second line"},
		{CutIndex: 3, StartSec: 8, DurationSec: 4}, // no dialogue, skipped
	}
	got := GenerateASS(cuts, ASSColors{})
	if !strings.Contains(got, "[Events]") {
		t.Fatal("missing [Events] section")
	}
	if !strings.Contains(got, "Karaoke") {
		t.Fatal("style name should be Karaoke")
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
	got := GenerateASS(cuts, ASSColors{})
	// Each lyric line must become its own Dialogue event (not joined with \N)
	if strings.Contains(got, `\N`) {
		t.Fatalf("newline should not be converted to \\N, got: %s", got)
	}
	if strings.Count(got, "Dialogue:") != 2 {
		t.Fatalf("expected 2 Dialogue events for 2 lyric lines, got: %s", got)
	}
	if !strings.Contains(got, "line one") || !strings.Contains(got, "line two") {
		t.Fatalf("lyric lines missing: %s", got)
	}
	// Each line gets half the duration (0-2s and 2-4s)
	if !strings.Contains(got, "0:00:00.00,0:00:02.00") {
		t.Fatalf("wrong timing for first line: %s", got)
	}
	if !strings.Contains(got, "0:00:02.00,0:00:04.00") {
		t.Fatalf("wrong timing for second line: %s", got)
	}
}

func TestGenerateASS_colors(t *testing.T) {
	cuts := []VideoHistoryCut{
		{CutIndex: 1, StartSec: 0, DurationSec: 4, Dialogue: "hello"},
	}
	// default: yellow primary, white secondary
	def := GenerateASS(cuts, ASSColors{})
	if !strings.Contains(def, "&H0000FFFF") {
		t.Fatalf("default primary should be yellow (&H0000FFFF), got: %s", def)
	}
	if !strings.Contains(def, "&H00FFFFFF") {
		t.Fatalf("default secondary should be white (&H00FFFFFF), got: %s", def)
	}

	// custom colors: red primary, blue secondary
	custom := GenerateASS(cuts, ASSColors{Primary: "#FF0000", Secondary: "#0000FF"})
	if !strings.Contains(custom, "&H000000FF") { // red in ASS = &H000000FF
		t.Fatalf("custom primary red not found, got: %s", custom)
	}
	if !strings.Contains(custom, "&H00FF0000") { // blue in ASS = &H00FF0000
		t.Fatalf("custom secondary blue not found, got: %s", custom)
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
