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
	// CommandShortVideoFromSection は、既存ジョブのレシピから指定セクションのカット群だけを
	// 動画化してショート動画を生成するコマンドです。
	CommandShortVideoFromSection TaskCommand = "short_video_from_section"

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
	// SectionIndex は動画化対象のセクション配列インデックス（0始まり）です（short_video_from_section コマンド専用）。
	// セクション名はサビ等で重複しうるため、名前ではなくインデックスで指定します。
	SectionIndex *int `json:"section_index,omitempty"`
	// VeoModel が空でないとき、動画生成に使う Veo モデルをタスク単位で差し替えます。
	VeoModel string `json:"veo_model,omitempty"`
	// VeoAspectRatio が空でないとき、動画のアスペクト比（"16:9" または "9:16"）をタスク単位で差し替えます。
	VeoAspectRatio string `json:"veo_aspect_ratio,omitempty"`
	// OverwriteKeyframe が true のとき、再生成したキーフレームでレシピを上書きします（regenerate_cut_keyframe コマンド専用）。
	OverwriteKeyframe bool `json:"overwrite_keyframe,omitempty"`
	// OriginalJobID は、再生成タスクの結果が実際に書き込まれる元ジョブのIDです（regenerate_cut_keyframe / regenerate_zip コマンド専用）。
	// JobID は新規生成されたタスク自身のIDのため、通知等で参照先の History Detail を示すにはこちらを使います。
	OriginalJobID string `json:"original_job_id,omitempty"`
	// VisualAnchorOverride が空でないとき、再生成対象カットのビジュアルアンカー（プロンプト文言）をこの値に差し替えます（regenerate_cut_keyframe コマンド専用）。
	// EditPrompt が指定されている場合はそちらが優先され、VisualAnchorOverride は無視されます。
	VisualAnchorOverride string `json:"visual_anchor_override,omitempty"`
	// EditPrompt が空でないとき、フル再生成ではなく既存キーフレーム画像を編集する「編集モード」になります
	// （regenerate_cut_keyframe コマンド専用）。構図・ポーズ・背景は保ったまま、この指示内容だけを反映します。
	EditPrompt string `json:"edit_prompt,omitempty"`
	// SeedOverride が非nilのとき、再生成に使うキャラクターシードを一時的にこの値へ差し替えます（regenerate_cut_keyframe コマンド専用）。
	SeedOverride *int64 `json:"seed_override,omitempty"`
	// SeedOverrideCharacterID は、SeedOverride を適用する対象キャラクターIDです。
	// ハンドラーが再生成対象カットの既存 CharacterID を解決して設定します（regenerate_cut_keyframe コマンド専用）。
	SeedOverrideCharacterID string `json:"seed_override_character_id,omitempty"`
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
	case CommandShortVideoFromSection:
		if t.Recipe == nil && t.VideoRecipe == nil && strings.TrimSpace(t.RecipeURL) == "" {
			return fmt.Errorf("%s task requires recipe, video_recipe, or recipe_url", t.Command)
		}
		if t.SectionIndex == nil || *t.SectionIndex < 0 {
			return fmt.Errorf("%s task requires a non-negative section_index", t.Command)
		}
		if err := validateOptionalGCSURI("recipe_url", t.RecipeURL); err != nil {
			return err
		}
		if err := validateOptionalAspectRatio(t.VeoAspectRatio); err != nil {
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

// validateOptionalAspectRatio validates an optional Veo aspect ratio field.
func validateOptionalAspectRatio(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || value == "16:9" || value == "9:16" {
		return nil
	}
	return fmt.Errorf("veo_aspect_ratio must be 16:9 or 9:16")
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
