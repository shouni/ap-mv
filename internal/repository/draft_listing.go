package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"

	"github.com/shouni/go-job-kit/paging"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-mv/internal/domain"
)

// このファイルは VideoRecipe 下書きの一覧・取得・削除を集めています。
// 走査するプレフィックスが違うだけで仕組みは履歴一覧と同じですが、下書きは
// キーフレームも動画も持たないため、表示モデル・キャッシュキー・削除範囲を分けています。

// collectDraftJobIDs は draftBaseURI 直下を走査して下書きのジョブ ID を集めます。
func (r *VideoHistoryRepository) collectDraftJobIDs(ctx context.Context) ([]string, error) {
	prefix := r.draftBaseURI + "/"
	seen := map[string]bool{}
	var jobIDs []string
	err := r.reader.List(ctx, prefix, func(gcsPath string) error {
		if path.Base(gcsPath) != domain.VideoDraftFileName {
			return nil
		}
		// {draftBaseURI}/{jobID}/video_recipe_draft.json の1階層のみ対象にする。
		rel := strings.TrimPrefix(gcsPath, r.draftBaseURI+"/")
		if strings.Count(rel, "/") != 1 {
			return nil
		}
		jobID := path.Base(path.Dir(gcsPath))
		if jobID == "." || jobID == "/" || jobID == "" || seen[jobID] {
			return nil
		}
		if err := jobid.Validate(jobID); err != nil {
			return nil
		}
		seen[jobID] = true
		jobIDs = append(jobIDs, jobID)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list draft objects: %w", err)
	}
	return jobIDs, nil
}

// ListDraftPage は VideoRecipe 下書きの一覧をページングして取得します。
//
// 履歴一覧と違い、読み込めなかった下書きは一覧から落とします。履歴はレシピが読めなくても
// 動画やキーフレームが残っている可能性がありますが、下書きの中身はレシピそのものなので、
// 見出しだけ並べても操作のしようがありません。
func (r *VideoHistoryRepository) ListDraftPage(ctx context.Context, page int, perPage int) (domain.VideoDraftPage, error) {
	if r == nil || r.reader == nil || r.draftBaseURI == "" {
		return domain.VideoDraftPage{}, nil
	}
	jobIDs, err := r.listJobIDs(ctx, draftJobIDListCacheKey, r.collectDraftJobIDs)
	if err != nil {
		return domain.VideoDraftPage{}, err
	}

	load := func(ctx context.Context, id string) (domain.VideoDraft, error) {
		recipe, err := r.GetDraftRecipe(ctx, id)
		if err != nil {
			slog.WarnContext(ctx, "failed to load draft recipe",
				"job_id", id,
				"error", err,
			)
			return domain.VideoDraft{}, err
		}
		return videoDraftFromRecipe(id, r.draftURI(id), *recipe), nil
	}

	// 並び順の理由は ListHistoryPage と同じ（ジョブ ID の文字列比較では用途プレフィックス順に
	// なるため、埋め込まれた時刻をソートキーに使う）。
	drafts, meta, err := paging.LoadPage(ctx, jobIDs, page, perPage, load,
		paging.WithSortKey(jobid.SortKey),
		paging.WithConcurrency(historyFetchConcurrency),
	)
	if err != nil {
		return domain.VideoDraftPage{}, err
	}

	return domain.VideoDraftPage{Items: drafts, PageMeta: meta}, nil
}

// videoDraftFromRecipe は下書きレシピを一覧表示用のモデルへ変換します。
func videoDraftFromRecipe(jobID string, storageURI string, recipe domain.VideoRecipe) domain.VideoDraft {
	draft := domain.VideoDraft{
		JobID:            jobID,
		Title:            strings.TrimSpace(firstNonEmpty(recipe.MusicRecipe.Title, recipe.ProjectTitle)),
		Mood:             strings.TrimSpace(recipe.MusicRecipe.Mood),
		Tempo:            recipe.MusicRecipe.Tempo,
		CreatedAt:        formatHistoryCreatedAt(jobID),
		VisualMode:       strings.TrimSpace(recipe.MusicRecipe.ComposeMode),
		CutCount:         len(recipe.Cuts),
		TotalDurationSec: domain.TotalDurationSecOfCuts(recipe.Cuts),
		SectionCount:     len(recipe.MusicRecipe.Sections),
		AspectRatio:      strings.TrimSpace(recipe.AspectRatio),
		StorageURI:       storageURI,
	}
	if draft.Title == "" {
		draft.Title = jobID
	}
	return draft
}

// GetDraftRecipe は下書きプレフィックスから VideoRecipe を読み出します。
//
// GetHistory と別メソッドにしているのは、呼び出し側に読み先を選ばせるためです。完成ジョブ側に
// 無ければ下書き側を見る、という書き方にすると分けたはずの名前空間が読み出しで混ざり、
// 「この ID はどちらのものか」がコードから消えます。
//
// 下書きは進行中に書き換わらない（作られたら以後変わらない）ので、履歴レシピと違って
// キャッシュの陳腐化を気にする必要がありません。ただし一覧走査は1ページあたり数件しか
// 読まないため、レシピ本体はキャッシュしていません。
func (r *VideoHistoryRepository) GetDraftRecipe(ctx context.Context, jobID string) (*domain.VideoRecipe, error) {
	if r == nil || r.reader == nil || r.draftBaseURI == "" {
		return nil, errors.New("draft repository is not properly configured")
	}
	if err := jobid.Validate(jobID); err != nil {
		return nil, err
	}
	uri := r.draftURI(jobID)
	rc, err := r.reader.Open(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("open draft (%s): %w", uri, err)
	}
	defer func() { _ = rc.Close() }()

	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read draft (%s): %w", uri, err)
	}
	recipe, err := domain.DecodeVideoRecipeJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("decode draft (%s): %w", uri, err)
	}
	return recipe, nil
}

// SaveDraftRecipe は下書きの VideoRecipe を上書き保存します。
//
// 下書きを読んで直して読み直す、を画像コスト0で繰り返せるようにするための経路です。
// 直した内容を反映する手段が生成時の recipe_json しか無いと、レビューは1周で終わってしまい、
// 「確認してから焼く」ための下書きが確認のためだけの読み取り専用になります。
//
// 保存前に Normalize と検証を通すのは DraftSaveFilter と同じ理由です（下書きとしては
// 読めるがキーフレーム生成で落ちるレシピを一覧に残さない）。一覧の表示値はレシピから
// 都度算出するため、キャッシュの破棄は要りません（下書き本体はキャッシュしていません）。
func (r *VideoHistoryRepository) SaveDraftRecipe(ctx context.Context, jobID string, recipe *domain.VideoRecipe) error {
	if r == nil || r.writer == nil || r.draftBaseURI == "" {
		return errors.New("draft repository is not properly configured")
	}
	if err := jobid.Validate(jobID); err != nil {
		return err
	}
	if recipe == nil {
		return errors.New("draft recipe is nil")
	}
	recipe.Normalize()
	if err := domain.ValidateVideoRecipe(recipe); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(recipe, "", "  ")
	if err != nil {
		return fmt.Errorf("encode draft recipe: %w", err)
	}
	uri := r.draftURI(jobID)
	if err := r.writer.Write(ctx, uri, bytes.NewReader(raw), remoteio.WithContentType("application/json")); err != nil {
		return fmt.Errorf("write draft (%s): %w", uri, err)
	}
	return nil
}

// DeleteDraft は指定ジョブ ID の下書きを削除します。
//
// 下書きは安く作れるぶん溜まるので、消す手段は一覧と同時に要ります。
//
// 消す先が2つあるのは、下書きジョブも進行状況を記録するためです。成果物（下書き JSON）は
// drafts プレフィックス配下ですが、status.json はジョブ状態ストアの決まりで jobs プレフィックス
// 配下（`<jobs>/<jobID>/`）に書かれます。前者だけ消すと、履歴にも下書き一覧にも現れない
// ディレクトリが jobs 配下に残り続けます。
func (r *VideoHistoryRepository) DeleteDraft(ctx context.Context, jobID string) error {
	if r == nil || r.reader == nil || r.writer == nil || r.draftBaseURI == "" {
		return errors.New("draft repository is not properly configured")
	}
	if err := jobid.Validate(jobID); err != nil {
		return err
	}
	paths, err := r.listObjectsUnder(ctx, r.draftBaseURI+"/"+jobID+"/")
	if err != nil {
		return fmt.Errorf("list draft objects for deletion: %w", err)
	}
	if len(paths) == 0 {
		paths = append(paths, r.draftURI(jobID))
	}
	// 下書きジョブが jobs 配下へ残した進行状況（status.json）も引き取る。ファイル名を
	// 決め打ちせずプレフィックスごと拾うのは、状態ストアの保存先が変わっても追随するためです。
	if r.baseURI != "" {
		statusPaths, err := r.listObjectsUnder(ctx, r.baseURI+"/"+jobID+"/")
		if err != nil {
			return fmt.Errorf("list draft job status for deletion: %w", err)
		}
		paths = append(paths, statusPaths...)
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
	// 一覧から消えたことを TTL の満了を待たずに反映させる。
	r.invalidateJobIDList(draftJobIDListCacheKey)
	return nil
}

// listObjectsUnder は prefix 配下のオブジェクト URI を集めます。
func (r *VideoHistoryRepository) listObjectsUnder(ctx context.Context, prefix string) ([]string, error) {
	var paths []string
	if err := r.reader.List(ctx, prefix, func(gcsPath string) error {
		paths = append(paths, gcsPath)
		return nil
	}); err != nil {
		return nil, err
	}
	return paths, nil
}

// draftURI は下書き JSON の GCS URI を返します。
func (r *VideoHistoryRepository) draftURI(jobID string) string {
	return r.draftBaseURI + "/" + jobID + "/" + domain.VideoDraftFileName
}
