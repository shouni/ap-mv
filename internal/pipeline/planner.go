package pipeline

import (
	"fmt"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/pipeline/step"
	"github.com/shouni/ap-mv/internal/ports"
)

// Planner は、タスクに応じて実行すべきステップ列（実行計画）を決定します。
// Runner は計画の取得と順次実行だけを担い、コマンドごとのステップ構成の知識は
// Planner 側に集約されます。
type Planner interface {
	Plan(task *domain.Task, videoRunner ports.VideoRunner) ([]step.Step, error)
}

// DefaultPlanner は、タスクコマンドごとの標準ステップ列を返す本番用の Planner です。
type DefaultPlanner struct {
	// UsePreviousVideo は VEO_USE_PREVIOUS_VIDEO 設定を反映します。
	UsePreviousVideo bool
	// VideoProcessor は、継続チェーンのフレーム引き継ぎ・ハードカット結合に使います。
	VideoProcessor ports.VideoProcessor
}

// Plan はタスクコマンドに応じた標準ステップ列を返します。各ケースがそのコマンドの
// 完全なステップ列を返すため、コマンドごとの違いは1箇所を見れば分かります。
//
// video_recipe_create はスクリプト生成から始め、recipe 入力系は既存 recipe の読み込みから始めます。
// video_recipe_create はキーフレーム生成までで停止し、動画生成と公開は実行しません。
// video_recipe_draft はさらに手前、カット割り（scene_split）までで停止して VideoRecipe を
// 下書き保存します。キーフレーム画像はカット数だけ焼かれるため、その手前に確認の余地を残します。
// regenerate_cut_keyframe は指定カット 1 枚、regenerate_section_keyframes は指定セクションの
// 全カットのキーフレームのみ再生成します。
// video_gen_continuation は VideoGenerationStep が生成済み VideoRecipe を引き継いで内部的に
// enqueue するコマンドのため、scripting/keyframe/zip/section-select は再実行しません。継続は
// Command を上書きしてしまうので、元の計画は OriginCommand から復元します。
//
// 動画を作る計画は「どの範囲を作るか」と「結合するか」の2軸で分かれます。
// mv_from_keyframe_video_recipe（全カット）・regenerate_cut_video（1カット）・
// short_video_from_section（セクションを独立ショートとして新ジョブへ）はいずれも結合まで
// 通しますが、section_video は結合しません。1本の MV をセクションずつ積み上げる操作なので、
// 焼くたびに結合すると final_video_url が「途中まで繋がった動画」になり、完成品と区別が
// つかなくなるためです。仕上げは finalize_video が担当し、こちらは生成を一切行いません。
//
// 未知のコマンドは（domain.Task.Validate が先に弾くのが原則ですが、Validate にだけ追加されて
// 実行計画が未登録の新コマンドが黙って既定のチェーンへ流れないよう）明示的にエラーを返します。
func (p DefaultPlanner) Plan(task *domain.Task, videoRunner ports.VideoRunner) ([]step.Step, error) {
	videoGen := step.VideoGenerationStep{Runner: videoRunner, UsePreviousVideo: p.UsePreviousVideo, VideoProcessor: p.VideoProcessor}
	sceneSplit := step.SceneSplitStep{UsePreviousVideo: p.UsePreviousVideo, Runner: videoRunner}
	// chainFinalize は全カット生成完了後（videoGenがErrPipelineDeferredを返さず正常終了した
	// 回のみ）に1度だけ実行され、継続チェーンをハードカットで1本の完成動画へ結合します。
	chainFinalize := step.ChainFinalizeStep{VideoProcessor: p.VideoProcessor, UsePreviousVideo: p.UsePreviousVideo}
	// section_video は結合しない代わりに生成対象をセクションへ絞ります。レシピは削らず、
	// 対象外のカットを飛ばすだけです（VideoGenerationStep.SectionScoped 参照）。
	sectionVideoGen := videoGen
	sectionVideoGen.SectionScoped = true
	switch command := taskCommand(task); command {
	case domain.CommandVideoGenContinuation:
		// 継続タスクは元のコマンドの実行計画を引き継ぎます。Command は上書きされているため、
		// 「どのコマンドの続きか」は OriginCommand でしか分かりません。
		if originCommand(task) == domain.CommandSectionVideo {
			// section_video の続きも結合しません。ここで chainFinalize を通すと、
			// セクションを1つ焼き終えるたびに中途半端な final_video_url が生まれます。
			return []step.Step{
				step.OriginalJobOutputStep{},
				sectionVideoGen,
				step.PublishingStep{},
			}, nil
		}
		return []step.Step{videoGen, chainFinalize, step.PublishingStep{}}, nil
	case domain.CommandSectionVideo:
		// 「キーフレーム → 動画」をセクション単位で一続きに進める計画です。出力先を元ジョブへ
		// 戻したうえで、キーフレームは足りないぶんだけ焼き、動画はそのセクションだけ生成し、
		// レシピ**全体**を元ジョブへ保存し直します。結合は finalize_video が別途担当します。
		return []step.Step{
			step.RecipeLoadStep{},
			step.OriginalJobOutputStep{},
			step.SectionKeyframeStep{},
			step.ZipUploadStep{},
			sectionVideoGen,
			step.PublishingStep{},
		}, nil
	case domain.CommandFinalizeVideo:
		// 生成を一切行わず、生成済みカットを1本へ結合し直すだけの計画です。
		// videoGen を通さないため、未生成カットが残っていても課金は発生しません。
		return []step.Step{
			step.RecipeLoadStep{},
			step.OriginalJobOutputStep{},
			chainFinalize,
			step.PublishingStep{},
		}, nil
	case domain.CommandRegenerateCutKeyframe, domain.CommandRegenerateSectionKeyframes:
		// 対象が1カットかセクション内の全カットかは RegenerateCutKeyframeStep が
		// Task の cut_index / section_index から解決するため、実行計画は共通です。
		return []step.Step{
			step.RecipeLoadStep{},
			step.RegenerateCutKeyframeStep{},
			step.ZipUploadStep{},
		}, nil
	case domain.CommandRegenerateZip:
		return []step.Step{
			step.RecipeLoadStep{},
			step.ZipUploadStep{},
		}, nil
	case domain.CommandRegenerateCutVideo:
		// scene_split を通さないのが要点。保存済みのカット割りをそのまま使い、
		// CutVideoSelectStep が対象カット（と同じチェーンの残り）だけを未生成へ戻す。
		// 他のカットは status=generated のままなので videoGen がスキップし、
		// chainFinalize が既存の動画と合わせて 1 本へ結合し直す。
		return []step.Step{
			step.RecipeLoadStep{},
			step.CutVideoSelectStep{UsePreviousVideo: p.UsePreviousVideo},
			videoGen,
			chainFinalize,
			step.PublishingStep{},
		}, nil
	case domain.CommandShortVideoFromSection:
		return []step.Step{
			step.RecipeLoadStep{},
			step.SectionSelectStep{},
			videoGen,
			chainFinalize,
			step.PublishingStep{},
		}, nil
	case domain.CommandVideoRecipeDraft:
		// キーフレームを1枚も焼かずに終わる唯一の生成系コマンド。scene_split まで通すのは
		// カット割りを確定させてから見せるためで、台本直後のカット列は尺が未確定です。
		// 保存先は完成ジョブと同じ video_music_meta.json で、段階は JobProgress が
		// カット数から導きます（script 段階として履歴に並びます）。
		return []step.Step{
			step.ScriptingStep{},
			sceneSplit,
			step.RecipeSaveStep{},
		}, nil
	case domain.CommandVideoRecipeCreate:
		return []step.Step{
			step.ScriptingStep{},
			sceneSplit,
			step.CutKeyframeStep{},
			step.ZipUploadStep{},
		}, nil
	case domain.CommandMVFromKeyframeVideoRecipe:
		return []step.Step{
			step.RecipeLoadStep{},
			sceneSplit,
			step.CutKeyframeStep{},
			step.ZipUploadStep{},
			videoGen,
			chainFinalize,
			step.PublishingStep{},
		}, nil
	default:
		return nil, fmt.Errorf("no step plan for command %q", command)
	}
}

// taskCommand は nil タスクを空コマンドとして扱い、Plan の switch を単純に保ちます。
func taskCommand(task *domain.Task) domain.TaskCommand {
	if task == nil {
		return ""
	}
	return task.Command
}

// originCommand は、継続タスクを生んだ元のコマンドを返します。空（旧タスクや継続以外）は
// 空コマンドとして扱い、従来どおりの実行計画へフォールバックします。
func originCommand(task *domain.Task) domain.TaskCommand {
	if task == nil {
		return ""
	}
	return task.OriginCommand
}

// StaticPlanner は、常に固定のステップ列を返すテスト用の Planner です。
type StaticPlanner []step.Step

// Plan は保持しているステップ列をそのまま返します。
func (p StaticPlanner) Plan(*domain.Task, ports.VideoRunner) ([]step.Step, error) {
	return p, nil
}
