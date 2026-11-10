package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// This file gathers the public entry points of history (listing, detail).
// For storage reads and signed URLs see history_storage.go; for converting status and recipes
// into display models see history_mapping.go.

const videoMetadataFile = "video_music_meta.json"

// listedCommands are the commands shown in the history listing.
//
// They used to be chosen by "the job's directory sits directly under baseURI and does not start
// with regen-keyframe-". Now that status lives in one collection, the filter does that job.
//
// The commands missing here (regenerate_cut_keyframe / regenerate_section_keyframes /
// regenerate_zip / section_video / finalize_video) are maintenance operations that write their
// artifacts back **to the original job**. They have no directory of their own, so they did not
// appear in the listing before either, and showing them would list rows with no artifacts.
// Their progress can be followed at /jobs/{jobID}.
//
// video_gen_continuation is not here. A continuation task overwrites Command, but what is recorded
// is the original command returned by domain.Task.ListedCommand.
var listedCommands = []string{
	string(domain.CommandVideoRecipeCreate),
	string(domain.CommandVideoRecipeDraft),
	string(domain.CommandMVFromKeyframeVideoRecipe),
	string(domain.CommandShortVideoFromSection),
	string(domain.CommandRegenerateCutVideo),
}

// VideoHistoryRepository returns the listing and details of generated MVs.
//
// The listing is a query on the job status collection; the detail is a read of the
// video_music_meta.json stored alongside the artifacts.
type VideoHistoryRepository struct {
	baseURI string
	// store is the window for reads, writes, listing and signing.
	store remoteio.Store
	// jobStatus is where the listing reads from. The headline has been copied into the status documents.
	jobStatus ports.JobStatusStore
}

// VideoHistoryRepositoryConfig is VideoHistoryRepository's dependencies.
//
// Dependencies are taken by name because with positional arguments there are places where a
// mix-up would still type-check the same way.
type VideoHistoryRepositoryConfig struct {
	// BaseURI is the root where artifacts go (a trailing slash is normalised).
	BaseURI string
	// Store is the window for reads, writes, listing and signing.
	// One is enough even for read-only uses.
	Store remoteio.Store
	// JobStatus is where the history listing reads from. When nil, the listing returns empty.
	JobStatus ports.JobStatusStore
}

// NewVideoHistoryRepository creates a generated MV history repository.
func NewVideoHistoryRepository(cfg VideoHistoryRepositoryConfig) *VideoHistoryRepository {
	return &VideoHistoryRepository{
		baseURI:   strings.TrimRight(strings.TrimSpace(cfg.BaseURI), "/"),
		store:     cfg.Store,
		jobStatus: cfg.JobStatus,
	}
}

// ListHistoryPage lists MV jobs with paging, optionally narrowed to one progress stage.
//
// No scan, no in-memory sort and no short-lived cache are needed; the query does that job.
// The total count comes from an aggregation query, so documents of jobs outside the page are not read.
// It used to collect every job ID under baseURI, sort them by the time in the ID, and open the
// video_music_meta.json of the displayed page in parallel. Filtering by stage went further and
// read every job's metadata, because the stage could only be derived from the cut list.
//
// An empty stage returns every job.
func (r *VideoHistoryRepository) ListHistoryPage(ctx context.Context, page int, perPage int, stage domain.JobStage) (domain.VideoHistoryPage, error) {
	if r == nil || r.jobStatus == nil {
		return domain.VideoHistoryPage{}, nil
	}

	opts := []jobstatus.ListOption{jobstatus.WithCommand(listedCommands...)}
	if stage != "" {
		// The path is the firestore tag names (domain.JobStatus.Progress → domain.JobProgress.Stage).
		opts = append(opts, jobstatus.WithField("progress.stage", string(stage)))
	}

	statuses, meta, err := r.jobStatus.List(ctx, page, perPage, opts...)
	if err != nil {
		return domain.VideoHistoryPage{}, fmt.Errorf("list job statuses: %w", err)
	}

	items := make([]domain.VideoHistory, 0, len(statuses))
	for _, status := range statuses {
		items = append(items, r.videoHistoryFromStatus(status))
	}
	return domain.VideoHistoryPage{Items: items, PageMeta: meta}, nil
}

// applyJobState fills in, from the status document, the job state the artifacts cannot tell.
//
// A failed job only leaves its artifacts partway, and the recipe says no more than "stopped at
// keyframes 3/12". Whether it failed or is rendering right now can only be told here.
//
// No record is normal (jobs from before this feature, the moment right after submission). Even
// when it cannot be read the detail itself is returned: only the state is missing, and showing
// the artifacts still works.
func (r *VideoHistoryRepository) applyJobState(ctx context.Context, jobID string, history *domain.VideoHistory) {
	if r.jobStatus == nil || history == nil {
		return
	}
	status, err := r.jobStatus.Get(ctx, jobID)
	if err != nil {
		if !errors.Is(err, domain.ErrJobStatusNotFound) {
			slog.WarnContext(ctx, "failed to load job status for history detail", "job_id", jobID, "error", err)
		}
		return
	}
	history.State = status.State
	history.Error = strings.TrimSpace(status.Error)
}

// GetHistory loads generated MV job metadata and cut keyframe references.
//
// Unlike the listing, it reads the artifact itself (video_music_meta.json). The listing holds
// only the headline copied into the status document, without where each cut's keyframe or video
// is. From the job status it fills in only the failure and its reason (applyJobState).
func (r *VideoHistoryRepository) GetHistory(ctx context.Context, jobID string) (domain.VideoHistoryDetail, error) {
	if r == nil || r.store == nil || r.baseURI == "" {
		return domain.VideoHistoryDetail{}, errors.New("history repository is not properly configured")
	}
	if err := jobid.Validate(jobID); err != nil {
		return domain.VideoHistoryDetail{}, err
	}
	recipe, err := r.fetchVideoRecipe(ctx, jobID)
	if err != nil {
		return domain.VideoHistoryDetail{}, err
	}
	history := r.buildHistoryFromFreshRecipe(ctx, jobID, recipe)
	r.applyJobState(ctx, jobID, &history)
	detail := domain.VideoHistoryDetail{
		VideoHistory: history,
		Cuts:         make([]domain.VideoHistoryCut, 0, len(recipe.Cuts)),
	}
	for _, cut := range recipe.Cuts {
		keyframeReference := r.resolveJobObjectURI(jobID, strings.TrimSpace(cut.KeyframeReference))
		detail.Cuts = append(detail.Cuts, domain.VideoHistoryCut{
			CutIndex:          cut.CutIndex,
			DurationSec:       cut.DurationSec,
			AudioCue:          strings.TrimSpace(cut.AudioCue),
			VisualAnchor:      strings.TrimSpace(cut.VisualAnchor),
			CharacterID:       strings.TrimSpace(cut.CharacterID),
			Dialogue:          strings.TrimSpace(cut.Dialogue),
			KeyframeReference: keyframeReference,
			VideoURL:          strings.TrimSpace(cut.VideoURL),
			Status:            strings.TrimSpace(string(cut.Status)),
			StartSec:          cut.StartSec,
			EndSec:            cut.EndSec,
		})
	}
	detail.Sections = historySectionsFromRecipe(recipe)
	// Nothing is signed here. The page follows same-origin paths, and exactly one URL is signed at
	// redirect time. Signing every cut would prepay dozens of IAM SignBlob round trips for one
	// detail page (Cloud Run has no private key, so signing is a network call, not a local computation).
	// Only JSON callers call SignHistoryURLs explicitly.
	return detail, nil
}
