package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shouni/go-utils/jobid"
)

// AIModels holds the models and seed used for text and image generation.
type AIModels struct {
	// TextModel is the model used for lyrics generation and recipe building (LLM)
	TextModel string `json:"text_model,omitempty"`
	// ImageModel is the model used for cover image generation
	ImageModel string `json:"image_model,omitempty"`
	Seed       *int64 `json:"seed,omitempty"`
}

// TaskCommand is the kind of task enqueued.
type TaskCommand string

const (
	// CommandVideoRecipeCreate creates a VideoRecipe.
	CommandVideoRecipeCreate TaskCommand = "video_recipe_create"
	// CommandVideoRecipeDraft runs script generation and cut splitting, then saves the
	// VideoRecipe as a draft without rendering a single keyframe. The number of cuts is the
	// number of keyframe images generated, so use it to check the allocation before going on.
	// The saved draft is passed to mv_from_keyframe_video_recipe as recipe_url.
	CommandVideoRecipeDraft TaskCommand = "video_recipe_draft"
	// CommandMVFromKeyframeVideoRecipe generates an MV from a VideoRecipe.
	CommandMVFromKeyframeVideoRecipe TaskCommand = "mv_from_keyframe_video_recipe"
	// CommandRegenerateCutKeyframe regenerates the keyframe of the given cut.
	CommandRegenerateCutKeyframe TaskCommand = "regenerate_cut_keyframe"
	// CommandRegenerateSectionKeyframes regenerates the keyframes of every cut in the given
	// section at once. Re-rendering one cut at a time leaves only one side of the division of
	// roles scene_split assigned within the section (scene beats such as establishing /
	// transition) with a new picture and breaks it, so a whole section can be re-rendered together.
	CommandRegenerateSectionKeyframes TaskCommand = "regenerate_section_keyframes"
	// CommandRegenerateZip regenerates the keyframe ZIP.
	CommandRegenerateZip TaskCommand = "regenerate_zip"
	// CommandRegenerateCutVideo remakes only the video of the given cut of an existing job.
	// The keyframes are the original job's as they are, and other cuts are skipped as already generated.
	// In the continuation-chain approach (VEO_USE_PREVIOUS_VIDEO=true), replacing the target cut's
	// video leaves the input of later cuts that reference it as PreviousVideoID stale, so the rest
	// of the same chain is remade too (CutVideoSelectFilter). The result is saved as a new job and
	// the original job is not changed.
	CommandRegenerateCutVideo TaskCommand = "regenerate_cut_video"
	// CommandShortVideoFromSection generates a short video by turning only the cuts of the
	// given section of an existing job's recipe into video.
	CommandShortVideoFromSection TaskCommand = "short_video_from_section"
	// CommandSectionVideo turns only the given section's cuts into video and **writes the result
	// back to the original job**. It is the operation for building an MV up one section at a time,
	// so when the visuals miss the intent the loss stays within that section.
	//
	// It differs from short_video_from_section in where it writes and how it cuts. A short makes a
	// standalone piece fitted into 60 seconds in a new job, so SectionSelectFilter replaces the
	// recipe's cut list itself with just that section. This one never trims the original recipe and
	// only "skips generation" of the cuts outside the target. Saving a trimmed recipe as-is would
	// delete the other sections' cuts, and since continuation tasks carry the recipe in the payload
	// (see enqueueContinuation), the trimmed state would be carried all the way to the final Publishing.
	//
	// It does not join (ChainFinalizeFilter). Re-joining every time a section is rendered would make
	// final_video_url a "video joined up to some point", impossible to tell from the finished one.
	// Once every section is in place, run finalize_video to put them into one.
	CommandSectionVideo TaskCommand = "section_video"
	// CommandFinalizeVideo re-joins the generated cut videos into one finished video.
	// It is the operation that finishes what section_video built up, and it generates nothing
	// (= no additional charge).
	CommandFinalizeVideo TaskCommand = "finalize_video"

	// CommandVideoGenContinuation is enqueued internally by VideoGenerationFilter to resume
	// per-cut video generation after a prior cut. It is never issued by HTTP handlers. Unlike
	// the original command it replaces, it skips scripting/keyframe/zip/section-select stages
	// (already applied to the carried VideoRecipe) so continuation only re-runs video
	// generation and publishing.
	CommandVideoGenContinuation TaskCommand = "video_gen_continuation"
)

// ListedCommand returns the command to record in the job status.
//
// A continuation task overwrites Command with video_gen_continuation, so recording it as-is
// loses the original command for exactly the jobs that went through a continuation. The history
// listing filters by command, so losing it drops a video_recipe_create job from the listing as
// soon as it goes through one continuation.
func (t *Task) ListedCommand() TaskCommand {
	if t == nil {
		return ""
	}
	if t.OriginCommand != "" {
		return t.OriginCommand
	}
	return t.Command
}

// Task is the payload of one video-generation task enqueued.
type Task struct {
	JobID   string      `json:"job_id"`
	Command TaskCommand `json:"command"`
	// OriginCommand is the command that spawned video_gen_continuation. A continuation task
	// overwrites Command, so without this "which command this continues" is lost and the
	// continuation cannot restore the execution plan (whether to join). Empty outside continuations.
	OriginCommand TaskCommand `json:"origin_command,omitempty"`
	AIModels
	SourceURL string `json:"source_url,omitempty"`
	Text      string `json:"text,omitempty"`
	ImageURL  string `json:"image_url,omitempty"`
	// VisualMode is the visual style chosen from the visual_modes prompts.
	VisualMode string `json:"visual_mode,omitempty"`
	// CharacterID is the character ID used for keyframe generation.
	CharacterID string `json:"character_id,omitempty"`
	// CutIndex is the index of the cut to regenerate (regenerate_cut_keyframe only).
	CutIndex *int `json:"cut_index,omitempty"`
	// SectionIndex is the target section's array index (0-based)
	// (short_video_from_section = what to turn into video / regenerate_section_keyframes = what to regenerate).
	// Section names can repeat (choruses and so on), so it is given by index rather than by name.
	SectionIndex *int `json:"section_index,omitempty"`
	// VeoModel, when non-empty, replaces the Veo model used for video generation for this task.
	VeoModel string `json:"veo_model,omitempty"`
	// VeoAspectRatio, when non-empty, sets the aspect ratio ("16:9" or "9:16") for this task.
	// When creating a new recipe (video_recipe_create / compose family) it is used for keyframe
	// generation and recorded on VideoRecipe.AspectRatio. For operations on an existing job
	// (video generation, cut regeneration) the handler sets the recorded VideoRecipe.AspectRatio
	// back here as-is, so keyframes and videos always share the aspect ratio.
	VeoAspectRatio string `json:"veo_aspect_ratio,omitempty"`
	// OverwriteKeyframe, when true, overwrites the recipe with the regenerated keyframe
	// (regenerate_cut_keyframe / regenerate_section_keyframes only).
	OverwriteKeyframe bool `json:"overwrite_keyframe,omitempty"`
	// OriginalJobID is the ID of the original job the regeneration task actually writes to
	// (regenerate_cut_keyframe / regenerate_section_keyframes / regenerate_zip only).
	// JobID is the newly minted task's own ID, so use this one to point notifications and the like at the right History Detail.
	OriginalJobID string `json:"original_job_id,omitempty"`
	// VisualAnchorOverride, when non-empty, replaces the target cut's visual anchor (the prompt wording) with this value (regenerate_cut_keyframe only).
	// The wording differs per cut, so it is not accepted for a section-wide regeneration.
	// When EditPrompt is set it wins and VisualAnchorOverride is ignored.
	VisualAnchorOverride string `json:"visual_anchor_override,omitempty"`
	// EditPrompt, when non-empty, switches to "edit mode", which edits the existing keyframe image rather than regenerating it
	// (regenerate_cut_keyframe / regenerate_section_keyframes only). Composition, pose and background are kept and
	// only this instruction is applied. For a section, the same instruction is applied to each cut in turn.
	EditPrompt string `json:"edit_prompt,omitempty"`
	// SeedOverride, when non-nil, temporarily replaces the character seed used for regeneration with this value
	// (regenerate_cut_keyframe / regenerate_section_keyframes only).
	SeedOverride *int64 `json:"seed_override,omitempty"`
	// SeedOverrideCharacterID is the character ID SeedOverride applies to.
	// The handler resolves the target cut's existing CharacterID and sets it
	// (regenerate_cut_keyframe / regenerate_section_keyframes only).
	SeedOverrideCharacterID string `json:"seed_override_character_id,omitempty"`
	// ASSPrimaryColor is the colour of sung syllables (CSS hex, e.g. "#FFFF00"). Empty means the default yellow.
	ASSPrimaryColor string `json:"ass_primary_color,omitempty"`
	// ASSSecondaryColor is the colour of not-yet-sung syllables (CSS hex, e.g. "#FFFFFF"). Empty means the default white.
	ASSSecondaryColor string       `json:"ass_secondary_color,omitempty"`
	RecipeURL         string       `json:"recipe_url,omitempty"`
	AudioURL          string       `json:"audio_url,omitempty"`
	Recipe            *MusicRecipe `json:"recipe,omitempty"`
	VideoRecipe       *VideoRecipe `json:"video_recipe,omitempty"`
	CreatedAt         time.Time    `json:"created_at"`
}

const gcsURIPrefix = "gs://"

// Validate checks the receiver for invalid state.
func (t *Task) Validate() error {
	if t == nil {
		return fmt.Errorf("task is nil")
	}
	if err := jobid.Validate(t.JobID); err != nil {
		return err
	}
	switch t.Command {
	case CommandVideoRecipeCreate, CommandVideoRecipeDraft:
		if strings.TrimSpace(t.SourceURL) == "" && strings.TrimSpace(t.Text) == "" && strings.TrimSpace(t.ImageURL) == "" {
			return fmt.Errorf("%s task requires source_url, text, or image_url", t.Command)
		}
		if err := validateOptionalGCSURI("source_url", t.SourceURL); err != nil {
			return err
		}
		if err := validateOptionalGCSURI("audio_url", t.AudioURL); err != nil {
			return err
		}
		return validateOptionalAspectRatio(t.VeoAspectRatio)
	case CommandMVFromKeyframeVideoRecipe, CommandVideoGenContinuation:
		if err := t.validateRecipeSourceTask(); err != nil {
			return err
		}
		if err := validateOptionalGCSURI("audio_url", t.AudioURL); err != nil {
			return err
		}
		if t.Recipe != nil {
			return ValidateMusicRecipe(t.Recipe)
		}
		return nil
	case CommandRegenerateCutKeyframe, CommandRegenerateCutVideo:
		if err := t.validateRecipeSourceTask(); err != nil {
			return err
		}
		if t.CutIndex == nil {
			return fmt.Errorf("%s task requires cut_index", t.Command)
		}
		return nil
	case CommandRegenerateSectionKeyframes, CommandShortVideoFromSection, CommandSectionVideo:
		if err := t.validateRecipeSourceTask(); err != nil {
			return err
		}
		if t.SectionIndex == nil || *t.SectionIndex < 0 {
			return fmt.Errorf("%s task requires a non-negative section_index", t.Command)
		}
		return nil
	case CommandFinalizeVideo:
		// It generates nothing, so all it needs is the recipe to finish.
		return t.validateRecipeSourceTask()
	case CommandRegenerateZip:
		if strings.TrimSpace(t.RecipeURL) == "" {
			return fmt.Errorf("%s task requires recipe_url", t.Command)
		}
		return validateOptionalGCSURI("recipe_url", t.RecipeURL)
	default:
		return fmt.Errorf("unsupported command: %s", t.Command)
	}
}

// validateRecipeSourceTask checks the triple shared by the commands that take a stored recipe
// as input (mv_from_keyframe_video_recipe / continuation / regeneration / short):
// "recipe ∨ video_recipe ∨ recipe_url is required → recipe_url is a GCS URI →
// aspect_ratio is an allowed value". Five cases used to repeat this sequence in full,
// leaving room for one place to miss a check whenever a command was added.
func (t *Task) validateRecipeSourceTask() error {
	if t.Recipe == nil && t.VideoRecipe == nil && strings.TrimSpace(t.RecipeURL) == "" {
		return fmt.Errorf("%s task requires recipe, video_recipe, or recipe_url", t.Command)
	}
	if err := validateOptionalGCSURI("recipe_url", t.RecipeURL); err != nil {
		return err
	}
	return validateOptionalAspectRatio(t.VeoAspectRatio)
}

// ASSColors returns the karaoke colors configured on this task.
func (t *Task) ASSColors() ASSColors {
	if t == nil {
		return ASSColors{}
	}
	return ASSColors{Primary: t.ASSPrimaryColor, Secondary: t.ASSSecondaryColor}
}

// AllowedAspectRatios is the single definition of the aspect ratios accepted for Veo generation.
// Config validation and task validation share it (keep compose.html's options in line with
// these values too — templates cannot reference constants, so
// TestComposeTemplateOffersAllAspectRatios checks that they agree).
var AllowedAspectRatios = []string{"16:9", "9:16"}

// DefaultAspectRatio is the aspect ratio used when neither the task nor the config gives one.
//
// go-veo-orchestrator no longer holds visual defaults (if the kit held one, it and
// VEO_ASPECT_RATIO would be two sources that silently disagree when only one changes),
// so the default is decided in this one place in the app.
const DefaultAspectRatio = "16:9"

// AllowedImageSizes is the single definition of the values accepted as keyframe output resolution.
// As with AllowedAspectRatios, go-veo-orchestrator no longer holds defaults, so the app owns
// this vocabulary.
var AllowedImageSizes = []string{"1K", "2K", "4K"}

// IsAllowedImageSize reports whether value is an allowed resolution.
func IsAllowedImageSize(value string) bool {
	return slices.Contains(AllowedImageSizes, value)
}

// IsAllowedAspectRatio reports whether value is an allowed aspect ratio.
func IsAllowedAspectRatio(value string) bool {
	return slices.Contains(AllowedAspectRatios, value)
}

// validateOptionalAspectRatio validates an optional Veo aspect ratio field.
func validateOptionalAspectRatio(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || IsAllowedAspectRatio(value) {
		return nil
	}
	return fmt.Errorf("veo_aspect_ratio must be one of %s", strings.Join(AllowedAspectRatios, ", "))
}

// validateOptionalGCSURI validates an optional GCS URI field.
func validateOptionalGCSURI(fieldName, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if !strings.HasPrefix(value, gcsURIPrefix) || strings.TrimSpace(strings.TrimPrefix(value, gcsURIPrefix)) == "" {
		return fmt.Errorf("%s must be a valid GCS URI (gs://...)", fieldName)
	}
	return nil
}
