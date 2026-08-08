package repository

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// DownloadKeyframes はジョブのキーフレーム画像を1枚ずつ sink へストリーミングします。
// キーフレームが存在するカットのみ対象で、ファイル名は cut_01.png 形式です。
func (r *VideoHistoryRepository) DownloadKeyframes(ctx context.Context, jobID string, sink ports.KeyframeSink) error {
	if r == nil || r.reader == nil || r.baseURI == "" {
		return errors.New("history repository is not properly configured")
	}
	if err := jobid.Validate(jobID); err != nil {
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
// どちらも組み立ての実体は domain 側（BuildFFmpegInputsTxt / GenerateASS）にあり、
// worker/filter の成果物 ZIP と同じ規則を共有します（以前はここに劣化コピーがあり、
// 空行を含む歌詞でカラオケのタイミングが本流とずれていました）。
func sinkFFmpegFiles(cuts []domain.VideoCut, bpm int, sink ports.KeyframeSink) error {
	if inputs := domain.BuildFFmpegInputsTxt(cuts); inputs != "" {
		if err := sink("inputs.txt", strings.NewReader(inputs)); err != nil {
			return fmt.Errorf("write inputs.txt: %w", err)
		}
	}

	if ass := domain.GenerateASS(domain.VideoCutsToHistoryCuts(cuts), domain.ASSColors{}, bpm); ass != "" {
		if err := sink("subtitles.ass", strings.NewReader(ass)); err != nil {
			return fmt.Errorf("write subtitles.ass: %w", err)
		}
	}
	return nil
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
	if err := jobid.Validate(jobID); err != nil {
		return err
	}
	paths, err := r.listObjectsUnder(ctx, r.baseURI+"/"+jobID+"/")
	if err != nil {
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
	// ジョブが一覧から消えたことを TTL の満了を待たずに反映させる。
	r.invalidateJobIDList(jobIDListCacheKey)
	return nil
}
