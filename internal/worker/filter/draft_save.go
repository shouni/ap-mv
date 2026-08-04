package filter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/ap-mv/internal/domain"
)

// DraftSaveFilter は、台本生成とカット割りを終えた VideoRecipe を下書きとして保存し、
// キーフレーム生成の手前でパイプラインを終わらせるステップです。
//
// 保存するのは SceneSplitFilter を通した後のレシピです。台本直後のカット列は尺が
// 確定しておらず（SceneSplit が達成可能なチェーン長へ割り付け、丸め誤差を次カットへ
// 送り、StartSec/EndSec を連結後の映像タイムラインへ振り直す）、それを見せても
// 実際に焼かれるカット割りとは別物になります。SceneSplitFilter は同じレシピを
// 二度通しても結果が変わらないため（TestSceneSplitFilterIsIdempotent）、この下書きを
// mv_from_keyframe_video_recipe へそのまま渡してもカット割りは保たれます。
type DraftSaveFilter struct{}

// Name returns the receiver name.
func (DraftSaveFilter) Name() string { return "draft_save" }

// Execute writes the current VideoRecipe to {draftPath}video_recipe_draft.json.
func (DraftSaveFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil || fc.VideoRecipe == nil {
		return fmt.Errorf("draft_save requires a video recipe")
	}
	if fc.Writer == nil {
		return fmt.Errorf("draft_save requires a writer")
	}
	draftPath := strings.TrimSpace(fc.DraftPath)
	if draftPath == "" {
		return fmt.Errorf("draft_save requires a draft path")
	}
	// 保存前に検証しておく。ここを通さないと、下書きとしては読めるがキーフレーム生成で
	// 落ちるレシピが一覧に残り、失敗の原因が下書き作成時まで遡らないと分からなくなる。
	if err := domain.ValidateVideoRecipe(fc.VideoRecipe); err != nil {
		return fmt.Errorf("draft_save: %w", err)
	}

	raw, err := json.MarshalIndent(fc.VideoRecipe, "", "  ")
	if err != nil {
		return fmt.Errorf("draft_save: encode video recipe: %w", err)
	}

	uri := strings.TrimRight(draftPath, "/") + "/" + domain.VideoDraftFileName
	if err := fc.Writer.Write(ctx, uri, bytes.NewReader(raw), remoteio.WithContentType("application/json")); err != nil {
		return fmt.Errorf("draft_save: write %s: %w", uri, err)
	}
	return nil
}
