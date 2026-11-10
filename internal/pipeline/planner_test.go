package pipeline

import (
	"testing"

	"github.com/shouni/ap-mv/internal/domain"
)

// TestDefaultPlannerCoversAllCommands is a table test pinning the step sequence of every
// TaskCommand. If a command is added but its plan is not registered in DefaultPlanner.Plan, it
// shows up when this test's exhaustive list is updated.
func TestDefaultPlannerCoversAllCommands(t *testing.T) {
	tests := []struct {
		command domain.TaskCommand
		// origin is the original command a continuation task inherits (empty otherwise).
		origin domain.TaskCommand
		want   []string
	}{
		{
			// The draft is the only generating command that ends without rendering a single keyframe. If
			// cut_keyframe_gen crept in, the draft's reason to exist, "check, then render", would vanish, so pin it here.
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
			// Section-wide regeneration has the same plan as regenerating one cut; only resolving the target
			// branches inside the regen_cut_keyframe step.
			command: domain.CommandRegenerateSectionKeyframes,
			want:    []string{"recipe_load", "regen_cut_keyframe", "zip_upload"},
		},
		{
			command: domain.CommandRegenerateZip,
			want:    []string{"recipe_load", "zip_upload"},
		},
		{
			// Remaking a cut's video does not go through scene_split. Re-planning the saved cut list would
			// break the correspondence with the videos of cuts not being remade (their existing VideoURL).
			command: domain.CommandRegenerateCutVideo,
			want:    []string{"recipe_load", "cut_video_select", "video_gen", "chain_finalize", "publishing"},
		},
		{
			command: domain.CommandShortVideoFromSection,
			want:    []string{"recipe_load", "section_select", "video_gen", "chain_finalize", "publishing"},
		},
		{
			// video_gen_continuation is the resume command VideoGenerationStep enqueues internally; the
			// carried VideoRecipe already reflects the scripting/keyframe/zip results, so it resumes from
			// video generation.
			command: domain.CommandVideoGenContinuation,
			want:    []string{"video_gen", "chain_finalize", "publishing"},
		},
		{
			// Building up per section. It skips scene_split for the same reason as cut_video: re-planning the
			// saved cut list would break the correspondence with existing cuts' videos.
			// The point of this plan is that chain_finalize is **not in it**; going through it would produce
			// a half-finished final_video_url every time a section is rendered.
			command: domain.CommandSectionVideo,
			want:    []string{"recipe_load", "original_job_output", "section_keyframe", "zip_upload", "video_gen", "publishing"},
		},
		{
			// A section_video continuation does not join either. If this fell to the plain continuation plan,
			// it would run through the join the moment a section finished rendering.
			command: domain.CommandVideoGenContinuation,
			origin:  domain.CommandSectionVideo,
			want:    []string{"original_job_output", "video_gen", "publishing"},
		},
		{
			// Finishing only. It skips video_gen, so nothing is charged even if ungenerated cuts remain.
			command: domain.CommandFinalizeVideo,
			want:    []string{"recipe_load", "original_job_output", "chain_finalize", "publishing"},
		},
	}
	for _, tt := range tests {
		name := string(tt.command)
		if tt.origin != "" {
			name += "_from_" + string(tt.origin)
		}
		t.Run(name, func(t *testing.T) {
			steps, err := DefaultPlanner{}.Plan(&domain.Task{Command: tt.command, OriginCommand: tt.origin}, nil)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			got := make([]string, 0, len(steps))
			for _, st := range steps {
				got = append(got, st.Name())
			}
			if len(got) != len(tt.want) {
				t.Fatalf("steps = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("steps = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestDefaultPlannerRejectsUnknownCommand checks that a command with no registered plan does not
// silently flow into the default chain but fails with an explicit error.
func TestDefaultPlannerRejectsUnknownCommand(t *testing.T) {
	if _, err := (DefaultPlanner{}).Plan(&domain.Task{Command: "no_such_command"}, nil); err == nil {
		t.Fatal("Plan() error = nil, want error for unknown command")
	}
	if _, err := (DefaultPlanner{}).Plan(nil, nil); err == nil {
		t.Fatal("Plan() error = nil, want error for nil task")
	}
}
