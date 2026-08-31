package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/shouni/go-job-firestore/jobfirestore"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// このファイルは履歴の公開エントリポイント（一覧・詳細）を集めています。
// ストレージ読み込み・キャッシュ・署名URLは history_storage.go、
// recipe→表示モデルの純粋な変換は history_mapping.go を参照してください。

const videoMetadataFile = "video_music_meta.json"

// listedCommands は、履歴一覧に出すコマンドです。
//
// 以前は「ジョブのディレクトリが baseURI 直下にあり、regen-keyframe- で始まらないこと」で
// 選んでいました。状態が 1 つのコレクションに集まった今は、絞り込みがその役目を持ちます。
//
// ここに無いコマンド（regenerate_cut_keyframe / regenerate_section_keyframes /
// regenerate_zip / section_video / finalize_video）は、成果物を**元のジョブへ**書き戻す
// 保守操作です。自分のディレクトリを持たないので以前も一覧に現れず、出すと成果物の無い
// 行が並ぶことになります。進行状況は /jobs/{jobID} で追えます。
//
// video_gen_continuation はここにありません。継続タスクは Command を上書きしますが、
// 記録されるのは domain.Task.ListedCommand が返す元のコマンドです。
var listedCommands = []string{
	string(domain.CommandVideoRecipeCreate),
	string(domain.CommandVideoRecipeDraft),
	string(domain.CommandMVFromKeyframeVideoRecipe),
	string(domain.CommandShortVideoFromSection),
	string(domain.CommandRegenerateCutVideo),
}

// VideoHistoryRepository は、生成済み MV の一覧と詳細を返します。
//
// 一覧はジョブ状態のコレクションへのクエリ、詳細は成果物と同じ場所に置かれた
// video_music_meta.json の読み込みです。
type VideoHistoryRepository struct {
	baseURI string
	// store は読み書き・一覧・署名の窓口です。
	store remoteio.Store
	// jobStatus は一覧の引き先です。見出しは状態ドキュメントに写してあります。
	jobStatus ports.JobStatusStore
}

// VideoHistoryRepositoryConfig は VideoHistoryRepository の依存関係です。
//
// 依存を名前で受けるのは、位置引数だと取り違えても型が同じ方向へ通ってしまう
// 箇所があるためです。
type VideoHistoryRepositoryConfig struct {
	// BaseURI は成果物を置く起点です（末尾のスラッシュは正規化します）。
	BaseURI string
	// Store は読み書き・一覧・署名の窓口です。
	// 読み取り専用の用途でも 1 つで足ります。
	Store remoteio.Store
	// JobStatus は履歴一覧の引き先です。nil なら一覧は空を返します。
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
// 走査もメモリ上の並べ替えも短期キャッシュも要りません。クエリがその役目を果たします。
// 総件数は集計クエリで求めるため、ページの外にあるジョブのドキュメントは読みません。
// 以前は baseURI 配下のジョブ ID を全件集めて ID 内の時刻で並べ替え、表示するページ分の
// video_music_meta.json を並行に開いていました。段階での絞り込みに至っては、段階が
// カット列からしか導けないため全ジョブのメタデータを読んでいました。
//
// stage が空なら全ジョブを返します。
func (r *VideoHistoryRepository) ListHistoryPage(ctx context.Context, page int, perPage int, stage domain.JobStage) (domain.VideoHistoryPage, error) {
	if r == nil || r.jobStatus == nil {
		return domain.VideoHistoryPage{}, nil
	}

	opts := []jobfirestore.ListOption{jobfirestore.WithCommand(listedCommands...)}
	if stage != "" {
		// パスは firestore タグの名前です（domain.JobStatus.Progress → domain.JobProgress.Stage）。
		opts = append(opts, jobfirestore.WithField("progress.stage", string(stage)))
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

// applyJobState は、成果物からは分からないジョブの状態を状態ドキュメントから補います。
//
// 失敗したジョブは成果物が途中まで残るだけで、レシピを見ても「keyframes 3/12 で止まって
// いる」としか分かりません。失敗したのか、いま焼いている最中なのかは、ここでしか付きません。
//
// 記録が無いのは正常です（この機能より前のジョブ、投入直後の一瞬）。読めなかった場合も
// 詳細そのものは返します。状態が付かないだけで、成果物の表示は成立するためです。
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
// Unlike ListHistoryPage, this always reads storage directly rather than the TTL cache: a
// single-job read is cheap, and callers use GetHistory precisely to check the latest state
// right after a regenerate/edit job completes, when a stale cached copy would be most visible
// and confusing (and, under multiple running instances, cache invalidation from the worker
// instance that ran the job can't reach every other instance's in-memory cache anyway).
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
	// 署名はここでは行いません。画面は同一オリジンのパスを辿り、リダイレクトの時点で
	// 1 本だけ署名します。カット 1 本ごとに署名すると、詳細 1 画面で数十回の
	// IAM SignBlob 往復を前払いすることになります（Cloud Run は秘密鍵を持たないため、
	// 署名はローカル計算ではなくネットワーク呼び出しです）。
	// JSON の呼び出し元だけが SignHistoryURLs を明示的に呼びます。
	return detail, nil
}
