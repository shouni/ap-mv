package domain

import (
	"fmt"
	"math"
	"strings"
)

const assHeader = `[Script Info]
ScriptType: v4.00+
PlayResX: 1920
PlayResY: 1080
WrapStyle: 0

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,72,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,-1,0,0,0,100,100,0,0,1,3,1,2,10,10,60,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`

// GenerateASS builds an ASS subtitle file from a slice of VideoHistoryCut.
// Cuts without Dialogue are skipped. Returns empty string if no dialogue exists.
func GenerateASS(cuts []VideoHistoryCut) string {
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
		// Replace newlines with ASS soft line-break (\N)
		text := strings.ReplaceAll(dialogue, "\n", `\N`)
		lines = append(lines, fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,%s", assTime(start), assTime(end), text))
	}
	if len(lines) == 0 {
		return ""
	}
	return assHeader + strings.Join(lines, "\n") + "\n"
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
