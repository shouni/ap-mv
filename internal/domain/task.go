package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type AIModels struct {
	// TextModel は歌詞生成およびレシピ構築（LLM）に使用するモデル
	TextModel string `json:"text_model,omitempty"`
	// ImageModel はジャケット画像生成に使用するモデル
	ImageModel string `json:"image_model,omitempty"`
	Seed       *int64 `json:"seed,omitempty"`
}

type TaskCommand string

const (
	CommandCompose            TaskCommand = "compose"
	CommandComposeToKeyframe  TaskCommand = "compose_to_keyframe"
	CommandGenerateFromRecipe TaskCommand = "generate_from_recipe"
)

type Task struct {
	JobID   string      `json:"job_id"`
	Command TaskCommand `json:"command"`
	AIModels
	SourceURL string `json:"source_url,omitempty"`
	Text      string `json:"text,omitempty"`
	ImageURL  string `json:"image_url,omitempty"`
	// CharacterID はキーフレーム生成で使うキャラクターIDです。
	CharacterID string       `json:"character_id,omitempty"`
	RecipeURL   string       `json:"recipe_url,omitempty"`
	AudioURL    string       `json:"audio_url,omitempty"`
	Recipe      *MusicRecipe `json:"recipe,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
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
	case CommandCompose, CommandComposeToKeyframe:
		if strings.TrimSpace(t.SourceURL) == "" && strings.TrimSpace(t.Text) == "" && strings.TrimSpace(t.ImageURL) == "" {
			return fmt.Errorf("compose task requires source_url, text, or image_url")
		}
		if err := validateOptionalGCSURI("audio_url", t.AudioURL); err != nil {
			return err
		}
	case CommandGenerateFromRecipe:
		if t.Recipe == nil && strings.TrimSpace(t.RecipeURL) == "" {
			return fmt.Errorf("generate_from_recipe task requires recipe or recipe_url")
		}
		if err := validateOptionalGCSURI("recipe_url", t.RecipeURL); err != nil {
			return err
		}
		if err := validateOptionalGCSURI("audio_url", t.AudioURL); err != nil {
			return err
		}
		if t.Recipe != nil {
			return t.Recipe.Validate()
		}
	default:
		return fmt.Errorf("unsupported command: %s", t.Command)
	}
	return nil
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
