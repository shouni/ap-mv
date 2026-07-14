package pipeline

import (
	"fmt"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
	"github.com/shouni/ap-mv/internal/worker/filter"
)

// FilterPlanner は、タスクに応じて実行すべきフィルター列（実行計画）を決定します。
// Runner は計画の取得と順次実行だけを担い、コマンドごとのフィルター構成の知識は
// Planner 側に集約されます。
type FilterPlanner interface {
	Plan(task *domain.Task, videoRunner ports.VideoRunner) ([]filter.Filter, error)
}

// DefaultPlanner は、タスクコマンドごとの標準フィルター列を返す本番用の FilterPlanner です。
type DefaultPlanner struct {
	// UsePreviousVideo は VEO_USE_PREVIOUS_VIDEO 設定を反映します。
	UsePreviousVideo bool
	// VideoProcessor は、継続チェーンのフレーム引き継ぎ・ハードカット結合に使います。
	VideoProcessor ports.VideoProcessor
}

// Plan はタスクコマンドに応じた標準フィルター列を返します。各ケースがそのコマンドの
// 完全なフィルター列を返すため、コマンドごとの違いは1箇所を見れば分かります。
//
// compose/video_recipe_create 系はスクリプト生成から始め、recipe 入力系は既存 recipe の読み込みから始めます。
// video_recipe_create/compose_to_keyframe はキーフレーム生成までで停止し、動画生成と公開は実行しません。
// regenerate_cut_keyframe は指定カット 1 枚のキーフレームのみ再生成します。
// video_gen_continuation は VideoGenerationFilter が生成済み VideoRecipe を引き継いで内部的に
// enqueue するコマンドのため、scripting/keyframe/zip/section-select は再実行しません。
//
// 未知のコマンドは（domain.Task.Validate が先に弾くのが原則ですが、Validate にだけ追加されて
// 実行計画が未登録の新コマンドが黙って既定のチェーンへ流れないよう）明示的にエラーを返します。
func (p DefaultPlanner) Plan(task *domain.Task, videoRunner ports.VideoRunner) ([]filter.Filter, error) {
	videoGen := filter.VideoGenerationFilter{Runner: videoRunner, UsePreviousVideo: p.UsePreviousVideo, VideoProcessor: p.VideoProcessor}
	sceneSplit := filter.SceneSplitFilter{UsePreviousVideo: p.UsePreviousVideo}
	// chainFinalize は全カット生成完了後（videoGenがErrPipelineDeferredを返さず正常終了した
	// 回のみ）に1度だけ実行され、継続チェーンをハードカットで1本の完成動画へ結合します。
	chainFinalize := filter.ChainFinalizeFilter{VideoProcessor: p.VideoProcessor}
	switch command := taskCommand(task); command {
	case domain.CommandVideoGenContinuation:
		return []filter.Filter{videoGen, chainFinalize, filter.PublishingFilter{}}, nil
	case domain.CommandRegenerateCutKeyframe:
		return []filter.Filter{
			filter.RecipeLoadFilter{},
			filter.RegenerateCutKeyframeFilter{},
			filter.ZipUploadFilter{},
		}, nil
	case domain.CommandRegenerateZip:
		return []filter.Filter{
			filter.RecipeLoadFilter{},
			filter.ZipUploadFilter{},
		}, nil
	case domain.CommandShortVideoFromSection:
		return []filter.Filter{
			filter.RecipeLoadFilter{},
			filter.SectionSelectFilter{},
			videoGen,
			chainFinalize,
			filter.PublishingFilter{},
		}, nil
	case domain.CommandVideoRecipeCreate, domain.CommandComposeToKeyframe:
		return []filter.Filter{
			filter.ScriptingFilter{},
			sceneSplit,
			filter.CutKeyframeFilter{},
			filter.ZipUploadFilter{},
		}, nil
	case domain.CommandCompose:
		return []filter.Filter{
			filter.ScriptingFilter{},
			sceneSplit,
			filter.CutKeyframeFilter{},
			filter.ZipUploadFilter{},
			videoGen,
			chainFinalize,
			filter.PublishingFilter{},
		}, nil
	case domain.CommandMVFromKeyframeVideoRecipe, domain.CommandGenerateFromRecipe:
		return []filter.Filter{
			filter.RecipeLoadFilter{},
			sceneSplit,
			filter.CutKeyframeFilter{},
			filter.ZipUploadFilter{},
			videoGen,
			chainFinalize,
			filter.PublishingFilter{},
		}, nil
	default:
		return nil, fmt.Errorf("no filter plan for command %q", command)
	}
}

// taskCommand は nil タスクを空コマンドとして扱い、Plan の switch を単純に保ちます。
func taskCommand(task *domain.Task) domain.TaskCommand {
	if task == nil {
		return ""
	}
	return task.Command
}

// StaticPlanner は、常に固定のフィルター列を返すテスト用の FilterPlanner です。
type StaticPlanner []filter.Filter

// Plan は保持しているフィルター列をそのまま返します。
func (p StaticPlanner) Plan(*domain.Task, ports.VideoRunner) ([]filter.Filter, error) {
	return p, nil
}
