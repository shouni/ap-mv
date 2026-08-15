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
// video_recipe_create はスクリプト生成から始め、recipe 入力系は既存 recipe の読み込みから始めます。
// video_recipe_create はキーフレーム生成までで停止し、動画生成と公開は実行しません。
// video_recipe_draft はさらに手前、カット割り（scene_split）までで停止して VideoRecipe を
// 下書き保存します。キーフレーム画像はカット数だけ焼かれるため、その手前に確認の余地を残します。
// regenerate_cut_keyframe は指定カット 1 枚、regenerate_section_keyframes は指定セクションの
// 全カットのキーフレームのみ再生成します。
// video_gen_continuation は VideoGenerationFilter が生成済み VideoRecipe を引き継いで内部的に
// enqueue するコマンドのため、scripting/keyframe/zip/section-select は再実行しません。
//
// 未知のコマンドは（domain.Task.Validate が先に弾くのが原則ですが、Validate にだけ追加されて
// 実行計画が未登録の新コマンドが黙って既定のチェーンへ流れないよう）明示的にエラーを返します。
func (p DefaultPlanner) Plan(task *domain.Task, videoRunner ports.VideoRunner) ([]filter.Filter, error) {
	videoGen := filter.VideoGenerationFilter{Runner: videoRunner, UsePreviousVideo: p.UsePreviousVideo, VideoProcessor: p.VideoProcessor}
	sceneSplit := filter.SceneSplitFilter{UsePreviousVideo: p.UsePreviousVideo, Runner: videoRunner}
	// chainFinalize は全カット生成完了後（videoGenがErrPipelineDeferredを返さず正常終了した
	// 回のみ）に1度だけ実行され、継続チェーンをハードカットで1本の完成動画へ結合します。
	chainFinalize := filter.ChainFinalizeFilter{VideoProcessor: p.VideoProcessor, UsePreviousVideo: p.UsePreviousVideo}
	switch command := taskCommand(task); command {
	case domain.CommandVideoGenContinuation:
		return []filter.Filter{videoGen, chainFinalize, filter.PublishingFilter{}}, nil
	case domain.CommandRegenerateCutKeyframe, domain.CommandRegenerateSectionKeyframes:
		// 対象が1カットかセクション内の全カットかは RegenerateCutKeyframeFilter が
		// Task の cut_index / section_index から解決するため、実行計画は共通です。
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
	case domain.CommandRegenerateCutVideo:
		// scene_split を通さないのが要点。保存済みのカット割りをそのまま使い、
		// CutVideoSelectFilter が対象カット（と同じチェーンの残り）だけを未生成へ戻す。
		// 他のカットは status=generated のままなので videoGen がスキップし、
		// chainFinalize が既存の動画と合わせて 1 本へ結合し直す。
		return []filter.Filter{
			filter.RecipeLoadFilter{},
			filter.CutVideoSelectFilter{UsePreviousVideo: p.UsePreviousVideo},
			videoGen,
			chainFinalize,
			filter.PublishingFilter{},
		}, nil
	case domain.CommandShortVideoFromSection:
		return []filter.Filter{
			filter.RecipeLoadFilter{},
			filter.SectionSelectFilter{},
			videoGen,
			chainFinalize,
			filter.PublishingFilter{},
		}, nil
	case domain.CommandVideoRecipeDraft:
		// キーフレームを1枚も焼かずに終わる唯一の生成系コマンド。scene_split まで通すのは
		// カット割りを確定させてから見せるためで、台本直後のカット列は尺が未確定です。
		// 保存先は完成ジョブと同じ video_music_meta.json で、段階は JobProgress が
		// カット数から導きます（script 段階として履歴に並びます）。
		return []filter.Filter{
			filter.ScriptingFilter{},
			sceneSplit,
			filter.RecipeSaveFilter{},
		}, nil
	case domain.CommandVideoRecipeCreate:
		return []filter.Filter{
			filter.ScriptingFilter{},
			sceneSplit,
			filter.CutKeyframeFilter{},
			filter.ZipUploadFilter{},
		}, nil
	case domain.CommandMVFromKeyframeVideoRecipe:
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
