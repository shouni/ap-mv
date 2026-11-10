package repository

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// DownloadKeyframes streams the job's keyframe images to sink one at a time.
// Only cuts that have a keyframe are included, and file names look like cut_01.png.
func (r *VideoHistoryRepository) DownloadKeyframes(ctx context.Context, jobID string, sink ports.KeyframeSink) error {
	if r == nil || r.store == nil || r.baseURI == "" {
		return errors.New("history repository is not properly configured")
	}
	if err := jobid.Validate(jobID); err != nil {
		return err
	}
	recipe, err := r.fetchVideoRecipe(ctx, jobID)
	if err != nil {
		return err
	}
	// In case the stored recipe has no dialogue (old jobs included), assign the lyrics at
	// download time too.
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

// sinkFFmpegFiles passes inputs.txt (concat demuxer) and subtitles.ass (ASS karaoke subtitles) to sink.
// inputs.txt covers only cuts with a keyframe; subtitles.ass covers every cut with dialogue.
// Both are actually built on the domain side (BuildFFmpegInputsTxt / GenerateASS) and share the
// rules of worker/filter's artifact ZIP (there used to be a degraded copy here, and lyrics with
// blank lines threw the karaoke timing off from the main path).
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

// streamKeyframe reads one cut's keyframe and passes it to sink.
// It is split out of the loop body so defer reliably closes rc.
func (r *VideoHistoryRepository) streamKeyframe(ctx context.Context, uri string, cutIndex int, name string, sink ports.KeyframeSink) error {
	rc, err := r.store.Open(ctx, uri)
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
	if r == nil || r.store == nil || r.baseURI == "" {
		return nil
	}
	if err := jobid.Validate(jobID); err != nil {
		return err
	}
	if _, err := remoteio.DeletePrefix(ctx, r.store, r.baseURI+"/"+jobID+"/"); err != nil {
		return fmt.Errorf("delete history objects: %w", err)
	}
	// The listing is a job status query, so there is no cache to go around clearing.
	// The status document lives apart from the artifacts, and removing it is the handler's job
	// (handlers.deleteJobStatus).
	return nil
}
