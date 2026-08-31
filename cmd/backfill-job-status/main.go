// Command backfill-job-status は、Firestore へ移行する前に作られたジョブの状態
// ドキュメントを、GCS に残っている video_music_meta.json から書き起こします。
//
// 一覧はジョブ状態のクエリになったため、これを流さないと移行前のジョブが履歴から
// 消えます（成果物そのものは無傷で、/history/{jobID} を直接開けば読めます）。
//
// 一度きりの使い捨てです。既にドキュメントがあるジョブは触りません。上書きすると、
// 移行後に本物の記録が付いたジョブを、レシピから推測した値で塗り潰すことになります。
//
//	go run ./cmd/backfill-job-status -dry-run
//	go run ./cmd/backfill-job-status
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"os"
	"path"
	"strings"

	"github.com/shouni/go-job-firestore/jobfirestore"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-remote-io/remoteio/gcs"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/repository"
)

const videoMetadataFile = "video_music_meta.json"

// listedPrefixes は、ジョブ ID の用途プレフィックスから履歴一覧のコマンドを引きます。
//
// 移行前のジョブはコマンドを記録していないため、採番時のプレフィックスから戻します。
// ここに無いプレフィックス（regen-keyframe / regen-section / regen-zip / section-video /
// finalize）は成果物を元のジョブへ書き戻す保守操作で、以前も一覧に出ていません。
var listedPrefixes = map[string]domain.TaskCommand{
	"video-recipe": domain.CommandVideoRecipeCreate,
	"mv":           domain.CommandMVFromKeyframeVideoRecipe,
	"short":        domain.CommandShortVideoFromSection,
	"regen-video":  domain.CommandRegenerateCutVideo,
	// "recipe" は下書き（video_recipe_draft）と mv_from_keyframe_video_recipe の
	// 両方が使っており、ID だけでは分かりません。カットの進み具合で振り分けます
	// （commandForJob 参照）。どちらも一覧に出るコマンドなので、取り違えても
	// 一覧から消えることはありません。
	"recipe": domain.CommandVideoRecipeDraft,
}

func main() {
	dryRun := flag.Bool("dry-run", false, "書き込まずに、対象となるジョブだけを出力します")
	flag.Parse()

	if err := run(context.Background(), *dryRun); err != nil {
		slog.Error("backfill failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dryRun bool) error {
	cfg, err := config.LoadConfigFromEnv()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Storage.GCSBucket == "" || cfg.AI.VeoOutputPrefix == "" {
		return errors.New("AP_MV_BUCKET と VEO_OUTPUT_PREFIX が要ります")
	}
	baseURI := cfg.GetGCSObjectURL(path.Join(cfg.AI.VeoOutputPrefix, "jobs"))

	storage, err := gcs.New(ctx)
	if err != nil {
		return fmt.Errorf("create GCS factory: %w", err)
	}
	defer func() { _ = storage.Close() }()

	store, err := storage.Store()
	if err != nil {
		return fmt.Errorf("initialize GCS store: %w", err)
	}

	factory, err := jobfirestore.New(ctx,
		jobfirestore.WithProjectID(cfg.GCP.ProjectID),
		jobfirestore.WithDatabase(cfg.Storage.FirestoreDatabase),
	)
	if err != nil {
		return fmt.Errorf("initialize Firestore: %w", err)
	}
	defer func() { _ = factory.Close() }()

	client, err := factory.Client()
	if err != nil {
		return fmt.Errorf("obtain Firestore client: %w", err)
	}
	// コレクション名を知る場所を増やさないよう、本体と同じ構築を通します。
	statuses := repository.NewJobStatusRepository(client)

	jobIDs, err := collectJobIDs(ctx, store, baseURI)
	if err != nil {
		return err
	}
	slog.Info("scanned job directories", "base_uri", baseURI, "jobs", len(jobIDs))

	var written, skipped int
	for _, jobID := range jobIDs {
		ok, err := backfillJob(ctx, store, statuses, baseURI, jobID, dryRun)
		if err != nil {
			// 1 件の失敗で残りを止めません。移行の取りこぼしはもう一度流せば埋まります。
			slog.Warn("skipping job", "job_id", jobID, "error", err)
			skipped++
			continue
		}
		if ok {
			written++
			continue
		}
		skipped++
	}
	slog.Info("backfill finished", "written", written, "skipped", skipped, "dry_run", dryRun)
	return nil
}

// collectJobIDs は baseURI 直下のジョブディレクトリ名を集めます。
//
// 区切り文字を指定してジョブ 1 件を 1 エントリで受け取るので、配下の成果物は返りません。
func collectJobIDs(ctx context.Context, lister interface {
	List(ctx context.Context, name string, opts ...remoteio.ListOption) iter.Seq2[remoteio.Entry, error]
}, baseURI string,
) ([]string, error) {
	seen := map[string]bool{}
	var jobIDs []string
	for entry, err := range lister.List(ctx, baseURI+"/", remoteio.WithDelimiter("/")) {
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", baseURI, err)
		}
		if !entry.IsPrefix {
			continue
		}
		id := path.Base(strings.TrimSuffix(entry.Name, "/"))
		if id == "" || id == "." || id == "/" || seen[id] || !jobid.IsValid(id) {
			continue
		}
		seen[id] = true
		jobIDs = append(jobIDs, id)
	}
	return jobIDs, nil
}

// backfillJob は 1 件分の状態を書き起こします。書いたときだけ true を返します。
func backfillJob(
	ctx context.Context,
	store remoteio.Store,
	statuses *jobfirestore.Store[domain.JobStatus],
	baseURI, jobID string,
	dryRun bool,
) (bool, error) {
	prefix := listedPrefix(jobID)
	if prefix == "" {
		return false, nil
	}

	// 既にある記録は触りません。移行後に動いたジョブを推測値で塗り潰さないためです。
	if _, err := statuses.Get(ctx, jobID); err == nil {
		return false, nil
	} else if !errors.Is(err, jobfirestore.ErrNotFound) {
		return false, fmt.Errorf("read existing status: %w", err)
	}

	recipe, err := readRecipe(ctx, store, baseURI+"/"+jobID+"/"+videoMetadataFile)
	if err != nil {
		// メタデータの無いジョブは、レシピから写せる見出しが 1 つもありません。
		// 一覧に出しても題名も段階も空の行になるだけなので飛ばします。
		return false, err
	}

	status := domain.JobStatus{}
	status.JobID = jobID
	status.Command = string(commandForJob(prefix, recipe))
	status.State = domain.JobStateSucceeded
	if createdAt, err := jobid.CreatedAt(jobID); err == nil {
		status.QueuedAt = createdAt
		status.UpdatedAt = createdAt
	}
	status.ApplyVideoRecipe(recipe)

	if dryRun {
		slog.Info("would write status", "job_id", jobID, "command", status.Command, "stage", status.Progress.Stage)
		return true, nil
	}
	if err := statuses.Save(ctx, jobID, status); err != nil {
		return false, fmt.Errorf("save status: %w", err)
	}
	slog.Info("wrote status", "job_id", jobID, "command", status.Command, "stage", status.Progress.Stage)
	return true, nil
}

// listedPrefix は、ジョブ ID の用途プレフィックスのうち一覧に出るものを返します。
// 一覧に出ないジョブでは空を返します。
//
// 長いプレフィックスから先に見ます。"regen-video" は "regen" ではなく "regen-video" として
// 引く必要があり、短い方が先に当たると別のコマンドになります。
func listedPrefix(jobID string) string {
	best := ""
	for prefix := range listedPrefixes {
		if strings.HasPrefix(jobID, prefix+"-") && len(prefix) > len(best) {
			best = prefix
		}
	}
	return best
}

// commandForJob は、プレフィックスとレシピの進み具合からコマンドを決めます。
func commandForJob(prefix string, recipe *domain.VideoRecipe) domain.TaskCommand {
	command := listedPrefixes[prefix]
	// "recipe" は下書きと mv_from_keyframe_video_recipe の両方が使います。台本だけで
	// 止まっているものを下書き、キーフレーム以降へ進んだものを MV 生成とみなします。
	if prefix == "recipe" && domain.NewJobProgress(recipe.Cuts).Stage != domain.StageScript {
		return domain.CommandMVFromKeyframeVideoRecipe
	}
	return command
}

// readRecipe は video_music_meta.json を読んで VideoRecipe にします。
func readRecipe(ctx context.Context, store remoteio.Store, uri string) (*domain.VideoRecipe, error) {
	rc, err := store.Open(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", uri, err)
	}
	defer func() { _ = rc.Close() }()

	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", uri, err)
	}
	recipe, err := domain.DecodeVideoRecipeJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", uri, err)
	}
	return recipe, nil
}
