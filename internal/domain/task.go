package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// AIModels は、テキスト・画像生成に使用するモデルとシード値を保持します。
type AIModels struct {
	// TextModel は歌詞生成およびレシピ構築（LLM）に使用するモデル
	TextModel string `json:"text_model,omitempty"`
	// ImageModel はジャケット画像生成に使用するモデル
	ImageModel string `json:"image_model,omitempty"`
	Seed       *int64 `json:"seed,omitempty"`
}

// TaskCommand は、キューに投入されるタスクの種類を表します。
type TaskCommand string

const (
	// CommandCompose は、歌詞・レシピの作曲を行うコマンドです。
	CommandCompose TaskCommand = "compose"
	// CommandVideoRecipeCreate は、VideoRecipeを作成するコマンドです。
	CommandVideoRecipeCreate TaskCommand = "video_recipe_create"
	// CommandMVFromKeyframeVideoRecipe は、VideoRecipeからMVを生成するコマンドです。
	CommandMVFromKeyframeVideoRecipe TaskCommand = "mv_from_keyframe_video_recipe"
	// CommandRegenerateCutKeyframe は、指定カットのキーフレームを再生成するコマンドです。
	CommandRegenerateCutKeyframe TaskCommand = "regenerate_cut_keyframe"
	// CommandRegenerateZip は、キーフレームZIPを再生成するコマンドです。
	CommandRegenerateZip TaskCommand = "regenerate_zip"

	// CommandComposeToKeyframe is a legacy task command name kept for existing queued tasks and clients.
	CommandComposeToKeyframe TaskCommand = "compose_to_keyframe"
	// CommandGenerateFromRecipe is a legacy task command name kept for existing queued tasks and clients.
	CommandGenerateFromRecipe TaskCommand = "generate_from_recipe"
)

// Task は、キューに投入される動画生成タスク1件分のペイロードです。
type Task struct {
	JobID   string      `json:"job_id"`
	Command TaskCommand `json:"command"`
	AIModels
	SourceURL string `json:"source_url,omitempty"`
	Text      string `json:"text,omitempty"`
	ImageURL  string `json:"image_url,omitempty"`
	// VisualMode は visual_modes プロンプト群から選ぶ映像スタイルです。
	VisualMode string `json:"visual_mode,omitempty"`
	// CharacterID はキーフレーム生成で使うキャラクターIDです。
	CharacterID string `json:"character_id,omitempty"`
	// CutIndex は再生成対象のカットインデックスです（regenerate_cut_keyframe コマンド専用）。
	CutIndex *int `json:"cut_index,omitempty"`
	// OverwriteKeyframe が true のとき、再生成したキーフレームでレシピを上書きします（regenerate_cut_keyframe コマンド専用）。
	OverwriteKeyframe bool `json:"overwrite_keyframe,omitempty"`
	// ASSPrimaryColor は歌唱済みシラブルの色（CSS hex, e.g. "#FFFF00"）。空のときはデフォルト黄色。
	ASSPrimaryColor string `json:"ass_primary_color,omitempty"`
	// ASSSecondaryColor は未歌唱シラブルの色（CSS hex, e.g. "#FFFFFF"）。空のときはデフォルト白。
	ASSSecondaryColor string       `json:"ass_secondary_color,omitempty"`
	RecipeURL         string       `json:"recipe_url,omitempty"`
	AudioURL          string       `json:"audio_url,omitempty"`
	Recipe            *MusicRecipe `json:"recipe,omitempty"`
	VideoRecipe       *VideoRecipe `json:"video_recipe,omitempty"`
	CreatedAt         time.Time    `json:"created_at"`
}

var jobIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

const gcsURIPrefix = "gs://"

// NewJobID constructs a valid job ID with the given prefix.
func NewJobID(prefix string) (string, error) {
	prefix = strings.Trim(strings.ToLower(prefix), "_- ")
	if prefix == "" {
		prefix = "job"
	}
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("job id entropy: %w", err)
	}
	id := fmt.Sprintf("%s-%s-%s", prefix, time.Now().UTC().Format("20060102-150405"), hex.EncodeToString(buf))
	if err := ValidateJobID(id); err != nil {
		return "", err
	}
	return id, nil
}

// ValidateJobID validates a job ID string.
func ValidateJobID(jobID string) error {
	if !jobIDPattern.MatchString(jobID) {
		return fmt.Errorf("invalid job id: %q", jobID)
	}
	return nil
}

// Validate checks the receiver for invalid state.
func (t *Task) Validate() error {
	if t == nil {
		return fmt.Errorf("task is nil")
	}
	if err := ValidateJobID(t.JobID); err != nil {
		return err
	}
	switch t.Command {
	case CommandCompose, CommandVideoRecipeCreate, CommandComposeToKeyframe:
		if strings.TrimSpace(t.SourceURL) == "" && strings.TrimSpace(t.Text) == "" && strings.TrimSpace(t.ImageURL) == "" {
			return fmt.Errorf("%s task requires source_url, text, or image_url", t.Command)
		}
		if t.Command == CommandVideoRecipeCreate {
			if err := validateOptionalGCSURI("source_url", t.SourceURL); err != nil {
				return err
			}
		}
		if err := validateOptionalGCSURI("audio_url", t.AudioURL); err != nil {
			return err
		}
	case CommandMVFromKeyframeVideoRecipe, CommandGenerateFromRecipe:
		if t.Recipe == nil && t.VideoRecipe == nil && strings.TrimSpace(t.RecipeURL) == "" {
			return fmt.Errorf("%s task requires recipe, video_recipe, or recipe_url", t.Command)
		}
		if err := validateOptionalGCSURI("recipe_url", t.RecipeURL); err != nil {
			return err
		}
		if err := validateOptionalGCSURI("audio_url", t.AudioURL); err != nil {
			return err
		}
		if t.Recipe != nil {
			return ValidateMusicRecipe(t.Recipe)
		}
	case CommandRegenerateCutKeyframe:
		if t.Recipe == nil && t.VideoRecipe == nil && strings.TrimSpace(t.RecipeURL) == "" {
			return fmt.Errorf("%s task requires recipe, video_recipe, or recipe_url", t.Command)
		}
		if t.CutIndex == nil {
			return fmt.Errorf("%s task requires cut_index", t.Command)
		}
		if err := validateOptionalGCSURI("recipe_url", t.RecipeURL); err != nil {
			return err
		}
	case CommandRegenerateZip:
		if strings.TrimSpace(t.RecipeURL) == "" {
			return fmt.Errorf("%s task requires recipe_url", t.Command)
		}
		if err := validateOptionalGCSURI("recipe_url", t.RecipeURL); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported command: %s", t.Command)
	}
	return nil
}

// ASSColors returns the karaoke colors configured on this task.
func (t *Task) ASSColors() ASSColors {
	if t == nil {
		return ASSColors{}
	}
	return ASSColors{Primary: t.ASSPrimaryColor, Secondary: t.ASSSecondaryColor}
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
