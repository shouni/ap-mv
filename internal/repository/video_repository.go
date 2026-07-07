package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path"
	"strings"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// DownloadKeyframes はジョブのキーフレーム画像を1枚ずつ sink へストリーミングします。
// キーフレームが存在するカットのみ対象で、ファイル名は cut_01.png 形式です。
func (r *VideoHistoryRepository) DownloadKeyframes(ctx context.Context, jobID string, sink ports.KeyframeSink) error {
	if r == nil || r.reader == nil || r.baseURI == "" {
		return errors.New("history repository is not properly configured")
	}
	if err := domain.ValidateJobID(jobID); err != nil {
		return err
	}
	recipe, err := r.fetchVideoRecipe(ctx, jobID)
	if err != nil {
		return err
	}
	// 保存済みレシピに dialogue が入っていない場合（旧ジョブ含む）に備えて
	// ダウンロード時にも歌詞割り当てを実行する。
	domain.ApplyLyricsToVideoRecipeCuts(&recipe)

	for _, cut := range recipe.Cuts {
		ref := strings.TrimSpace(cut.KeyframeReference)
		if ref == "" {
			continue
		}
		uri := r.resolveJobObjectURI(jobID, ref)
		ext := path.Ext(uri)
		if ext == "" {
			ext = ".png"
		}
		name := fmt.Sprintf("cut_%02d%s", cut.CutIndex, ext)
		if err := r.streamKeyframe(ctx, uri, cut.CutIndex, name, sink); err != nil {
			return err
		}
	}
	return sinkFFmpegFiles(recipe.Cuts, recipe.MusicRecipe.Tempo, sink)
}

// sinkFFmpegFiles は inputs.txt (concat demuxer) と subtitles.ass (ASS カラオケ字幕) を sink へ渡します。
// inputs.txt はキーフレームが存在するカットのみ、subtitles.ass は台詞があるカット全て対象です。
func sinkFFmpegFiles(cuts []domain.VideoCut, bpm int, sink ports.KeyframeSink) error {
	var inputsBuf strings.Builder
	for _, cut := range cuts {
		ref := strings.TrimSpace(cut.KeyframeReference)
		if ref == "" {
			continue
		}
		ext := path.Ext(ref)
		if ext == "" {
			ext = ".png"
		}
		fmt.Fprintf(&inputsBuf, "file 'cut_%02d%s'\n", cut.CutIndex, ext)
		if cut.DurationSec > 0 {
			fmt.Fprintf(&inputsBuf, "duration %.3f\n", cut.DurationSec)
		}
		fmt.Fprintln(&inputsBuf)
	}
	if inputsBuf.Len() > 0 {
		if err := sink("inputs.txt", strings.NewReader(inputsBuf.String())); err != nil {
			return fmt.Errorf("write inputs.txt: %w", err)
		}
	}

	ass := buildASSSubtitles(cuts, bpm)
	if ass != "" {
		if err := sink("subtitles.ass", strings.NewReader(ass)); err != nil {
			return fmt.Errorf("write subtitles.ass: %w", err)
		}
	}
	return nil
}

// buildASSSubtitles は ASS 形式のカラオケ字幕文字列を生成します。
// 台詞がないカットはスキップします。
func buildASSSubtitles(cuts []domain.VideoCut, bpm int) string {
	// 台詞が1件もなければ空を返す
	hasDialogue := false
	for _, cut := range cuts {
		if strings.TrimSpace(cut.Dialogue) != "" {
			hasDialogue = true
			break
		}
	}
	if !hasDialogue {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Script Info]\n")
	sb.WriteString("ScriptType: v4.00+\n")
	sb.WriteString("PlayResX: 1920\n")
	sb.WriteString("PlayResY: 1080\n")
	sb.WriteString("Timer: 100.0000\n\n")

	sb.WriteString("[V4+ Styles]\n")
	sb.WriteString("Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	// PrimaryColour=黄色(ハイライト済み), SecondaryColour=白(未ハイライト)
	sb.WriteString("Style: Karaoke,Arial,64,&H0000FFFF,&H00FFFFFF,&H00000000,&H80000000,-1,0,0,0,100,100,0,0,1,3,1,2,10,10,80,1\n\n")

	sb.WriteString("[Events]\n")
	sb.WriteString("Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	for _, cut := range cuts {
		dialogue := strings.TrimSpace(cut.Dialogue)
		if dialogue == "" {
			continue
		}
		cutStart := cut.StartSec
		cutEnd := cut.EndSec
		if cutEnd <= cutStart {
			cutEnd = cutStart + cut.DurationSec
		}
		// Dialogue が複数行の場合は行ごとにエントリを分割して時間を均等配分する
		lines := strings.Split(dialogue, "\n")
		secPerLine := (cutEnd - cutStart) / float64(len(lines))
		for i, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			start := cutStart + float64(i)*secPerLine
			end := start + secPerLine
			text := buildKaraokeLine(line, secPerLine, bpm)
			fmt.Fprintf(&sb, "Dialogue: 0,%s,%s,Karaoke,,0,0,0,,%s\n",
				formatASSTime(start), formatASSTime(end), text)
		}
	}
	return sb.String()
}

// buildKaraokeLine は {\k} タグ付きのカラオケテキストを生成します。
// BPM が設定されている場合はハーフビート単位にスナップします。
func buildKaraokeLine(dialogue string, durationSec float64, bpm int) string {
	runes := []rune(dialogue)
	if len(runes) == 0 {
		return ""
	}
	totalCs := max(int(math.Round(durationSec*100)), len(runes))
	csPerChar := totalCs / len(runes)

	// BPM スナップ: ハーフビート単位（6000/bpm/2 cs）に丸める
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

// formatASSTime は秒数を ASS タイムスタンプ形式（H:MM:SS.cc）に変換します。
func formatASSTime(sec float64) string {
	totalCs := int(math.Round(sec * 100))
	cs := totalCs % 100
	totalSec := totalCs / 100
	h := totalSec / 3600
	m := (totalSec % 3600) / 60
	s := totalSec % 60
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}

// streamKeyframe は1カット分のキーフレームを読み取り sink へ渡します。
// defer で rc を確実に Close するためにループ本体から切り出しています。
func (r *VideoHistoryRepository) streamKeyframe(ctx context.Context, uri string, cutIndex int, name string, sink ports.KeyframeSink) error {
	rc, err := r.reader.Open(ctx, uri)
	if err != nil {
		return fmt.Errorf("open keyframe for cut %d: %w", cutIndex, err)
	}
	defer rc.Close()
	if err := sink(name, rc); err != nil {
		return fmt.Errorf("write keyframe for cut %d: %w", cutIndex, err)
	}
	return nil
}

// DeleteHistory deletes all stored objects under a generated MV job directory.
func (r *VideoHistoryRepository) DeleteHistory(ctx context.Context, jobID string) error {
	if r == nil || r.reader == nil || r.writer == nil || r.baseURI == "" {
		return nil
	}
	if err := domain.ValidateJobID(jobID); err != nil {
		return err
	}
	prefix := r.baseURI + "/" + jobID + "/"
	var paths []string
	if err := r.reader.List(ctx, prefix, func(gcsPath string) error {
		paths = append(paths, gcsPath)
		return nil
	}); err != nil {
		return fmt.Errorf("list history objects for deletion: %w", err)
	}
	if len(paths) == 0 {
		paths = append(paths, r.metadataURI(jobID))
	}

	var errs []error
	for _, p := range paths {
		if err := r.writer.Delete(ctx, p); err != nil {
			errs = append(errs, fmt.Errorf("delete %s: %w", p, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	r.deleteCachedHistory(jobID)
	r.deleteCachedVideoRecipe(jobID)
	return nil
}
