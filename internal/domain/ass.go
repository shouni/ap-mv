// Package domain は、動画生成ワークフローが扱う中心的なドメインモデル
// （音楽レシピ、タスク、履歴、字幕、通知）を定義します。
package domain

import (
	"fmt"
	"math"
	"strings"
)

// ASSColors holds the karaoke highlight colors for ASS subtitle generation.
// Both fields accept CSS hex color strings (e.g. "#FFFF00").
// Zero value uses the defaults: Primary=yellow (#FFFF00), Secondary=white (#FFFFFF).
type ASSColors struct {
	Primary   string // sung color, default yellow
	Secondary string // upcoming color, default white
}

func (c ASSColors) primaryASS() string {
	if s := hexColorToASS(c.Primary); s != "" {
		return s
	}
	return "&H0000FFFF" // yellow
}

func (c ASSColors) secondaryASS() string {
	if s := hexColorToASS(c.Secondary); s != "" {
		return s
	}
	return "&H00FFFFFF" // white
}

// hexColorToASS converts a CSS hex color (#RRGGBB) to an ASS color code (&H00BBGGRR).
func hexColorToASS(hex string) string {
	hex = strings.TrimPrefix(strings.ToUpper(hex), "#")
	if len(hex) != 6 {
		return ""
	}
	return fmt.Sprintf("&H00%s%s%s", hex[4:6], hex[2:4], hex[0:2])
}

func buildAssHeader(colors ASSColors) string {
	return fmt.Sprintf(`[Script Info]
ScriptType: v4.00+
PlayResX: 1920
PlayResY: 1080
WrapStyle: 0

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Karaoke,Arial,72,%s,%s,&H00000000,&H80000000,-1,0,0,0,100,100,0,0,1,3,1,2,10,10,60,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`, colors.primaryASS(), colors.secondaryASS())
}

// GenerateASS builds an ASS subtitle file from a slice of VideoHistoryCut.
// Each newline-separated lyric line within a cut becomes its own Dialogue event,
// with duration distributed equally across lines. Each Dialogue's text carries
// per-character {\k} karaoke timing tags (so the Karaoke style's SecondaryColour
// highlight can animate); when bpm > 0 the per-character durations are snapped to
// half-beat units. Returns empty string if no dialogue exists.
func GenerateASS(cuts []VideoHistoryCut, colors ASSColors, bpm int) string {
	var lines []string
	for _, cut := range cuts {
		dialogue := strings.TrimSpace(cut.Dialogue)
		if dialogue == "" {
			continue
		}
		start := cut.StartSec
		end := cut.EndSec
		if end <= start {
			end = start + cut.DurationSec
		}

		lyricLines := strings.Split(dialogue, "\n")
		var filtered []string
		for _, l := range lyricLines {
			if t := strings.TrimSpace(l); t != "" {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) == 0 {
			continue
		}

		durPerLine := (end - start) / float64(len(filtered))
		for i, text := range filtered {
			lineStart := start + float64(i)*durPerLine
			lineEnd := lineStart + durPerLine
			karaoke := BuildKaraokeLine(text, durPerLine, bpm)
			lines = append(lines, fmt.Sprintf("Dialogue: 0,%s,%s,Karaoke,,0,0,0,,%s", assTime(lineStart), assTime(lineEnd), karaoke))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return buildAssHeader(colors) + strings.Join(lines, "\n") + "\n"
}

// BuildKaraokeLine renders a single lyric line as ASS karaoke text with per-character
// {\k<centiseconds>} timing tags. Without these tags the Karaoke style's SecondaryColour
// highlight cannot animate. Each character is allotted an equal share of durationSec (in
// centiseconds); when bpm > 0 that per-character duration is snapped to half-beat units
// (3000/bpm cs) so the highlight advances on the beat, and the final character absorbs any
// rounding remainder so the tag durations sum back to the line duration. Returns "" for an
// empty line.
func BuildKaraokeLine(dialogue string, durationSec float64, bpm int) string {
	runes := []rune(dialogue)
	if len(runes) == 0 {
		return ""
	}
	totalCs := max(int(math.Round(durationSec*100)), len(runes))
	csPerChar := totalCs / len(runes)

	// BPM スナップ: ハーフビート単位（3000/bpm cs）に丸める
	if bpm > 0 {
		halfBeatCs := math.Round(3000.0 / float64(bpm))
		if halfBeatCs >= 1 {
			snapped := int(math.Round(float64(csPerChar)/halfBeatCs) * halfBeatCs)
			if snapped >= 1 {
				csPerChar = snapped
			}
		}
	}

	var sb strings.Builder
	remaining := totalCs
	for i, r := range runes {
		cs := csPerChar
		if i == len(runes)-1 {
			// 最後の文字に残り時間を全て割り当てて合計を合わせる
			cs = remaining
		}
		if cs < 1 {
			cs = 1
		}
		fmt.Fprintf(&sb, "{\\k%d}%c", cs, r)
		remaining -= csPerChar
	}
	return sb.String()
}

// assTime formats seconds as ASS timestamp H:MM:SS.cs
func assTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	cs := int(math.Round(sec * 100))
	h := cs / 360000
	cs %= 360000
	m := cs / 6000
	cs %= 6000
	s := cs / 100
	cs %= 100
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}
