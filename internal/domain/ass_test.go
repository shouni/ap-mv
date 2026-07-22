package domain

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// kTagRE matches an ASS karaoke timing tag like {\k42}.
var kTagRE = regexp.MustCompile(`\{\\k(\d+)\}`)

// stripKTags removes all {\k<n>} tags, recovering the underlying lyric text so
// substring assertions can be made against dialogue that carries karaoke tags.
func stripKTags(s string) string {
	return kTagRE.ReplaceAllString(s, "")
}

func TestGenerateASS_empty(t *testing.T) {
	if got := GenerateASS(nil, ASSColors{}, 0); got != "" {
		t.Fatalf("expected empty string for nil cuts, got %q", got)
	}
	cuts := []VideoHistoryCut{{CutIndex: 1, StartSec: 0, DurationSec: 4}}
	if got := GenerateASS(cuts, ASSColors{}, 0); got != "" {
		t.Fatalf("expected empty string when no dialogue, got %q", got)
	}
}

func TestGenerateASS_basic(t *testing.T) {
	cuts := []VideoHistoryCut{
		{CutIndex: 1, StartSec: 0, DurationSec: 4, Dialogue: "Hello world"},
		{CutIndex: 2, StartSec: 4, EndSec: 8, Dialogue: "Second line"},
		{CutIndex: 3, StartSec: 8, DurationSec: 4}, // no dialogue, skipped
	}
	got := GenerateASS(cuts, ASSColors{}, 0)
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
	// Every Dialogue text must carry per-character {\k} karaoke tags, else the
	// SecondaryColour highlight cannot animate.
	if !kTagRE.MatchString(got) {
		t.Fatalf("expected {\\k} karaoke tags, got: %s", got)
	}
	stripped := stripKTags(got)
	if !strings.Contains(stripped, "Hello world") || !strings.Contains(stripped, "Second line") {
		t.Fatalf("dialogue text missing after stripping k-tags: %s", got)
	}
	if strings.Contains(got, "CutIndex: 3") || strings.Count(got, "Dialogue:") != 2 {
		t.Fatal("empty-dialogue cut should be skipped")
	}
}

func TestGenerateASS_multilineDialogue(t *testing.T) {
	cuts := []VideoHistoryCut{
		{CutIndex: 1, StartSec: 0, DurationSec: 4, Dialogue: "line one\nline two"},
	}
	got := GenerateASS(cuts, ASSColors{}, 0)
	// Each lyric line must become its own Dialogue event (not joined with \N)
	if strings.Contains(got, `\N`) {
		t.Fatalf("newline should not be converted to \\N, got: %s", got)
	}
	if strings.Count(got, "Dialogue:") != 2 {
		t.Fatalf("expected 2 Dialogue events for 2 lyric lines, got: %s", got)
	}
	stripped := stripKTags(got)
	if !strings.Contains(stripped, "line one") || !strings.Contains(stripped, "line two") {
		t.Fatalf("lyric lines missing after stripping k-tags: %s", got)
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
	def := GenerateASS(cuts, ASSColors{}, 0)
	if !strings.Contains(def, "&H0000FFFF") {
		t.Fatalf("default primary should be yellow (&H0000FFFF), got: %s", def)
	}
	if !strings.Contains(def, "&H00FFFFFF") {
		t.Fatalf("default secondary should be white (&H00FFFFFF), got: %s", def)
	}

	// custom colors: red primary, blue secondary
	custom := GenerateASS(cuts, ASSColors{Primary: "#FF0000", Secondary: "#0000FF"}, 0)
	if !strings.Contains(custom, "&H000000FF") { // red in ASS = &H000000FF
		t.Fatalf("custom primary red not found, got: %s", custom)
	}
	if !strings.Contains(custom, "&H00FF0000") { // blue in ASS = &H00FF0000
		t.Fatalf("custom secondary blue not found, got: %s", custom)
	}
}

// sumKTags returns the sum of all {\k<n>} centisecond values in s.
func sumKTags(s string) int {
	total := 0
	for _, m := range kTagRE.FindAllStringSubmatch(s, -1) {
		// m[1] is guaranteed numeric by the regexp.
		n, _ := strconv.Atoi(m[1])
		total += n
	}
	return total
}

func TestGenerateASS_karaokeTags(t *testing.T) {
	// "abcd" over 4s with no BPM snapping: 400cs / 4 runes = 100cs each.
	cuts := []VideoHistoryCut{
		{CutIndex: 1, StartSec: 0, DurationSec: 4, Dialogue: "abcd"},
	}
	got := GenerateASS(cuts, ASSColors{}, 0)
	tags := kTagRE.FindAllStringSubmatch(got, -1)
	if len(tags) != 4 {
		t.Fatalf("expected 4 k-tags for 4 characters, got %d: %s", len(tags), got)
	}
	if !strings.Contains(got, "{\\k100}a") {
		t.Fatalf("expected {\\k100}a with even 100cs split, got: %s", got)
	}
	// The per-character durations must sum back to the line duration (400cs).
	if sum := sumKTags(got); sum != 400 {
		t.Fatalf("k-tag durations should sum to 400cs, got %d: %s", sum, got)
	}
}

func TestBuildKaraokeLine(t *testing.T) {
	// Empty line yields no tags.
	if got := BuildKaraokeLine("", 4, 120); got != "" {
		t.Fatalf("expected empty string for empty line, got %q", got)
	}

	// No BPM: 200cs over 2 runes = 100cs each.
	line := BuildKaraokeLine("ab", 2, 0)
	if line != "{\\k100}a{\\k100}b" {
		t.Fatalf("unexpected even split: %q", line)
	}

	// BPM snapping: at 120 BPM a half-beat is round(3000/120)=25cs. Four runes over
	// 4s give 100cs each, which snaps to round(100/25)*25=100cs — durations still
	// sum to the total, with the last char absorbing the remainder.
	snapped := BuildKaraokeLine("abcd", 4, 120)
	if n := len(kTagRE.FindAllString(snapped, -1)); n != 4 {
		t.Fatalf("expected 4 k-tags, got %d: %s", n, snapped)
	}
	if sum := sumKTags(snapped); sum != 400 {
		t.Fatalf("snapped k-tag durations should sum to 400cs, got %d: %s", sum, snapped)
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
