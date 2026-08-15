package pipeline

import (
	"testing"

	"github.com/shouni/ap-mv/internal/domain"
)

// TestDefaultPlannerCoversAllCommands は、全 TaskCommand のフィルター列を固定するテーブル
// テストです。コマンドを追加したのに DefaultPlanner.Plan へ実行計画を登録し忘れた場合、
// このテストの網羅リスト更新時に露見します。
func TestDefaultPlannerCoversAllCommands(t *testing.T) {
	tests := []struct {
		command domain.TaskCommand
		want    []string
	}{
		{
			// 下書きはキーフレームを1枚も焼かずに終わる唯一の生成系コマンド。cut_keyframe_gen が
			// 紛れ込むと「確認してから焼く」という下書きの存在理由が消えるため、ここで固定する。
			command: domain.CommandVideoRecipeDraft,
			want:    []string{"scripting", "scene_split", "recipe_save"},
		},
		{
			command: domain.CommandVideoRecipeCreate,
			want:    []string{"scripting", "scene_split", "cut_keyframe_gen", "zip_upload"},
		},
		{
			command: domain.CommandMVFromKeyframeVideoRecipe,
			want:    []string{"recipe_load", "scene_split", "cut_keyframe_gen", "zip_upload", "video_gen", "chain_finalize", "publishing"},
		},
		{
			command: domain.CommandRegenerateCutKeyframe,
			want:    []string{"recipe_load", "regen_cut_keyframe", "zip_upload"},
		},
		{
			// セクション一括再生成はカット1枚の再生成と同じ実行計画で、対象の解決だけが
			// regen_cut_keyframe フィルター内で分岐します。
			command: domain.CommandRegenerateSectionKeyframes,
			want:    []string{"recipe_load", "regen_cut_keyframe", "zip_upload"},
		},
		{
			command: domain.CommandRegenerateZip,
			want:    []string{"recipe_load", "zip_upload"},
		},
		{
			// カット単位の動画作り直しは scene_split を通さない。保存済みのカット割りを
			// 再計画すると、作り直さないカットの動画（既存の VideoURL）と対応が崩れる。
			command: domain.CommandRegenerateCutVideo,
			want:    []string{"recipe_load", "cut_video_select", "video_gen", "chain_finalize", "publishing"},
		},
		{
			command: domain.CommandShortVideoFromSection,
			want:    []string{"recipe_load", "section_select", "video_gen", "chain_finalize", "publishing"},
		},
		{
			// video_gen_continuation は VideoGenerationFilter が内部的に enqueue する再開用
			// コマンドで、引き継いだ VideoRecipe に scripting/keyframe/zip の結果が反映済みの
			// ため動画生成から再開します。
			command: domain.CommandVideoGenContinuation,
			want:    []string{"video_gen", "chain_finalize", "publishing"},
		},
	}
	for _, tt := range tests {
		t.Run(string(tt.command), func(t *testing.T) {
			filters, err := DefaultPlanner{}.Plan(&domain.Task{Command: tt.command}, nil)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			got := make([]string, 0, len(filters))
			for _, flt := range filters {
				got = append(got, flt.Name())
			}
			if len(got) != len(tt.want) {
				t.Fatalf("filters = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("filters = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestDefaultPlannerRejectsUnknownCommand は、実行計画が未登録のコマンドが既定のチェーンへ
// 黙って流れず、明示的にエラーになることを検証します。
func TestDefaultPlannerRejectsUnknownCommand(t *testing.T) {
	if _, err := (DefaultPlanner{}).Plan(&domain.Task{Command: "no_such_command"}, nil); err == nil {
		t.Fatal("Plan() error = nil, want error for unknown command")
	}
	if _, err := (DefaultPlanner{}).Plan(nil, nil); err == nil {
		t.Fatal("Plan() error = nil, want error for nil task")
	}
}
