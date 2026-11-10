package pipeline

import (
	"fmt"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/pipeline/step"
	"github.com/shouni/ap-mv/internal/ports"
)

// Planner decides the step sequence (execution plan) to run for a task.
// Runner only fetches the plan and runs it in order; the knowledge of which steps each
// command consists of is gathered in Planner.
type Planner interface {
	Plan(task *domain.Task, videoRunner ports.VideoRunner) ([]step.Step, error)
}

// DefaultPlanner is the production Planner that returns the standard step sequence per task command.
type DefaultPlanner struct {
	// UsePreviousVideo reflects the VEO_USE_PREVIOUS_VIDEO setting.
	UsePreviousVideo bool
	// VideoProcessor is used for frame hand-over in continuation chains and for hard-cut joins.
	VideoProcessor ports.VideoProcessor
}

// Plan returns the standard step sequence for the task command. Each case returns the
// command's complete step sequence, so the differences between commands are visible in one place.
//
// video_recipe_create starts from script generation, while the recipe-input commands start by loading an existing recipe.
// video_recipe_create stops after keyframe generation and runs neither video generation nor publishing.
// video_recipe_draft stops even earlier, after cut splitting (scene_split), and saves the VideoRecipe
// as a draft. Keyframe images are rendered once per cut, so this leaves room to check before that.
// regenerate_cut_keyframe regenerates only the given cut's keyframe, regenerate_section_keyframes
// only the keyframes of every cut in the given section.
// video_gen_continuation is the command VideoGenerationStep enqueues internally, carrying the
// generated VideoRecipe on, so scripting/keyframe/zip/section-select are not re-run. A continuation
// overwrites Command, so the original plan is restored from OriginCommand.
//
// Plans that make video split along two axes: "which range to make" and "whether to join".
// mv_from_keyframe_video_recipe (every cut), regenerate_cut_video (one cut) and
// short_video_from_section (a section as a standalone short in a new job) all go through to the
// join, but section_video does not. It builds one MV up a section at a time, so joining after
// every render would make final_video_url a "video joined up to some point", impossible to tell
// from the finished one. finalize_video does the finishing, and generates nothing.
//
// An unknown command returns an explicit error (domain.Task.Validate is meant to reject it first,
// but this keeps a new command added only to Validate, with no registered plan, from silently
// flowing into the default chain).
func (p DefaultPlanner) Plan(task *domain.Task, videoRunner ports.VideoRunner) ([]step.Step, error) {
	videoGen := step.VideoGenerationStep{Runner: videoRunner, UsePreviousVideo: p.UsePreviousVideo, VideoProcessor: p.VideoProcessor}
	sceneSplit := step.SceneSplitStep{UsePreviousVideo: p.UsePreviousVideo, Runner: videoRunner}
	// chainFinalize runs exactly once after every cut is generated (only on the run where videoGen
	// finished normally rather than returning ErrPipelineDeferred) and joins the continuation chain into one finished video with hard cuts.
	chainFinalize := step.ChainFinalizeStep{VideoProcessor: p.VideoProcessor, UsePreviousVideo: p.UsePreviousVideo}
	// section_video does not join; instead it narrows generation to the section. The recipe is not
	// trimmed; cuts outside the target are only skipped (see VideoGenerationStep.SectionScoped).
	sectionVideoGen := videoGen
	sectionVideoGen.SectionScoped = true
	switch command := taskCommand(task); command {
	case domain.CommandVideoGenContinuation:
		// A continuation task carries on the original command's plan. Command has been overwritten,
		// so "which command this continues" is known only from OriginCommand.
		if originCommand(task) == domain.CommandSectionVideo {
			// A section_video continuation does not join either. Going through chainFinalize here would
			// produce a half-finished final_video_url every time a section finished rendering.
			return []step.Step{
				step.OriginalJobOutputStep{},
				sectionVideoGen,
				step.PublishingStep{},
			}, nil
		}
		return []step.Step{videoGen, chainFinalize, step.PublishingStep{}}, nil
	case domain.CommandSectionVideo:
		// The plan that advances "keyframes → video" in one go per section. With the output pointed
		// back at the original job, it renders only the missing keyframes, generates video for that
		// section only, and saves the **whole** recipe back to the original job. finalize_video joins separately.
		return []step.Step{
			step.RecipeLoadStep{},
			step.OriginalJobOutputStep{},
			step.SectionKeyframeStep{},
			step.ZipUploadStep{},
			sectionVideoGen,
			step.PublishingStep{},
		}, nil
	case domain.CommandFinalizeVideo:
		// A plan that generates nothing and only re-joins the generated cuts into one.
		// It does not go through videoGen, so nothing is charged even if ungenerated cuts remain.
		return []step.Step{
			step.RecipeLoadStep{},
			step.OriginalJobOutputStep{},
			chainFinalize,
			step.PublishingStep{},
		}, nil
	case domain.CommandRegenerateCutKeyframe, domain.CommandRegenerateSectionKeyframes:
		// Whether the target is one cut or every cut in a section is resolved by RegenerateCutKeyframeStep
		// from the Task's cut_index / section_index, so the plan is shared.
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
		// The point is not going through scene_split. The saved cut list is used as-is, and
		// CutVideoSelectStep resets only the target cut (and the rest of its chain) to ungenerated.
		// The other cuts stay status=generated, so videoGen skips them and
		// chainFinalize re-joins them with the existing videos into one.
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
		// The only generating command that ends without rendering a single keyframe. It goes through
		// scene_split so the cut list is settled before being shown; right after the script, the cuts'
		// durations are not settled yet. It is saved to the same video_music_meta.json as a finished job,
		// and JobProgress derives the stage from the cut count (it is listed in history at the script stage).
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

// taskCommand treats a nil task as the empty command, keeping Plan's switch simple.
func taskCommand(task *domain.Task) domain.TaskCommand {
	if task == nil {
		return ""
	}
	return task.Command
}

// originCommand returns the command that spawned a continuation task. Empty (old tasks or
// non-continuations) is treated as the empty command and falls back to the usual plan.
func originCommand(task *domain.Task) domain.TaskCommand {
	if task == nil {
		return ""
	}
	return task.OriginCommand
}

// StaticPlanner is a test Planner that always returns a fixed step sequence.
type StaticPlanner []step.Step

// Plan returns the step sequence it holds as-is.
func (p StaticPlanner) Plan(*domain.Task, ports.VideoRunner) ([]step.Step, error) {
	return p, nil
}
