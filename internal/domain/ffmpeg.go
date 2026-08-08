package domain

import (
	"fmt"
	"path"
	"strings"
)

// VideoCutsToHistoryCuts は、レシピのカット列を ASS 字幕生成が受け取る
// VideoHistoryCut 列へ写します。ASS 生成（GenerateASS）が使うフィールドだけを
// 持ち回るための変換で、repository（キーフレーム ZIP）と worker/filter
// （成果物 ZIP）の両方がこれを通ることで、字幕のタイミング規則が 1 実装に揃います。
func VideoCutsToHistoryCuts(cuts []VideoCut) []VideoHistoryCut {
	result := make([]VideoHistoryCut, 0, len(cuts))
	for _, c := range cuts {
		result = append(result, VideoHistoryCut{
			CutIndex:    c.CutIndex,
			DurationSec: c.DurationSec,
			Dialogue:    c.Dialogue,
			StartSec:    c.StartSec,
			EndSec:      c.EndSec,
		})
	}
	return result
}

// BuildFFmpegInputsTxt は、キーフレームを持つカットから ffmpeg concat demuxer 用の
// inputs.txt を組み立てます。ファイル名はカット番号 + 参照の拡張子（無ければ .png）で、
// ZIP 内に保存されるキーフレームのファイル名と一致させる必要があります。
// 同じ形式を repository と worker/filter の 2 箇所で別々に書いていたため、
// 定義をここに 1 つにしています。
func BuildFFmpegInputsTxt(cuts []VideoCut) string {
	var sb strings.Builder
	for _, cut := range cuts {
		ref := strings.TrimSpace(cut.KeyframeReference)
		if ref == "" {
			continue
		}
		ext := path.Ext(ref)
		if ext == "" {
			ext = ".png"
		}
		fmt.Fprintf(&sb, "file 'cut_%02d%s'\n", cut.CutIndex, ext)
		if cut.DurationSec > 0 {
			fmt.Fprintf(&sb, "duration %.3f\n", cut.DurationSec)
		}
		fmt.Fprintln(&sb)
	}
	return sb.String()
}
