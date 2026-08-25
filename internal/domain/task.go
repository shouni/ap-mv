package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shouni/go-utils/jobid"
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
	// CommandVideoRecipeCreate は、VideoRecipeを作成するコマンドです。
	CommandVideoRecipeCreate TaskCommand = "video_recipe_create"
	// CommandVideoRecipeDraft は、台本生成とカット割りまでを実行し、キーフレームを
	// 1枚も焼かずに VideoRecipe を下書きとして保存するコマンドです。カット数がそのまま
	// キーフレーム画像の生成枚数になるため、割り付けを確認してから進めたいときに使います。
	// 保存した下書きは recipe_url として mv_from_keyframe_video_recipe へ渡します。
	CommandVideoRecipeDraft TaskCommand = "video_recipe_draft"
	// CommandMVFromKeyframeVideoRecipe は、VideoRecipeからMVを生成するコマンドです。
	CommandMVFromKeyframeVideoRecipe TaskCommand = "mv_from_keyframe_video_recipe"
	// CommandRegenerateCutKeyframe は、指定カットのキーフレームを再生成するコマンドです。
	CommandRegenerateCutKeyframe TaskCommand = "regenerate_cut_keyframe"
	// CommandRegenerateSectionKeyframes は、指定セクションに属する全カットのキーフレームを
	// まとめて再生成するコマンドです。1カットずつ焼き直すと、scene_split が付けたセクション内の
	// 役割分担（establishing / transition といった scene beat）が片方だけ新しい絵になって
	// 崩れるため、セクション単位でまとめて焼き直せるようにしています。
	CommandRegenerateSectionKeyframes TaskCommand = "regenerate_section_keyframes"
	// CommandRegenerateZip は、キーフレームZIPを再生成するコマンドです。
	CommandRegenerateZip TaskCommand = "regenerate_zip"
	// CommandRegenerateCutVideo は、既存ジョブのうち指定カットの動画だけを作り直すコマンドです。
	// キーフレームは元ジョブのものをそのまま使い、他のカットは生成済みのままスキップされます。
	// 継続チェーン方式（VEO_USE_PREVIOUS_VIDEO=true）では、対象カットの動画を差し替えると
	// それを PreviousVideoID として参照する後続カットの入力が古くなるため、同じチェーンの
	// 残りもまとめて作り直します（CutVideoSelectFilter）。結果は新しいジョブとして保存され、
	// 元ジョブは変更しません。
	CommandRegenerateCutVideo TaskCommand = "regenerate_cut_video"
	// CommandShortVideoFromSection は、既存ジョブのレシピから指定セクションのカット群だけを
	// 動画化してショート動画を生成するコマンドです。
	CommandShortVideoFromSection TaskCommand = "short_video_from_section"
	// CommandSectionVideo は、指定セクションのカットだけを動画化し、結果を**元のジョブへ
	// 書き戻す**コマンドです。MV を 1 セクションずつ積み上げるための操作で、画作りが
	// 意図と違ったときの損失がそのセクション分に留まります。
	//
	// short_video_from_section との違いは出力先と切り出し方です。ショートは新しいジョブへ
	// 60 秒に収めた独立作品を作るため、SectionSelectFilter がレシピのカット列そのものを
	// そのセクションだけに差し替えます。こちらは元のレシピを一切削らず、対象外のカットを
	// 「生成をスキップする」だけに留めます。削ったレシピをそのまま保存すると他セクションの
	// カットが消えるうえ、継続タスクはレシピをペイロードで持ち回る（enqueueContinuation
	// 参照）ため、削れた状態が最後の Publishing まで運ばれてしまいます。
	//
	// 結合（ChainFinalizeFilter）は実行しません。セクションを 1 つ焼くたびに結合し直すと
	// final_video_url が「途中まで繋がった動画」になり、完成品と見分けが付かなくなります。
	// 全セクションが揃ったところで finalize_video を実行して 1 本にまとめます。
	CommandSectionVideo TaskCommand = "section_video"
	// CommandFinalizeVideo は、生成済みカットの動画を 1 本の完成動画へ結合し直すコマンドです。
	// section_video で積み上げた結果を仕上げるための操作で、生成は一切行いません
	// （＝追加の課金は発生しません）。
	CommandFinalizeVideo TaskCommand = "finalize_video"

	// CommandVideoGenContinuation is enqueued internally by VideoGenerationFilter to resume
	// per-cut video generation after a prior cut. It is never issued by HTTP handlers. Unlike
	// the original command it replaces, it skips scripting/keyframe/zip/section-select stages
	// (already applied to the carried VideoRecipe) so continuation only re-runs video
	// generation and publishing.
	CommandVideoGenContinuation TaskCommand = "video_gen_continuation"
)

// Task は、キューに投入される動画生成タスク1件分のペイロードです。
type Task struct {
	JobID   string      `json:"job_id"`
	Command TaskCommand `json:"command"`
	// OriginCommand は、video_gen_continuation を生んだ元のコマンドです。継続タスクは
	// Command を上書きしてしまうため、これが無いと「どのコマンドの続きなのか」が失われ、
	// 実行計画（結合するか否か）を継続側で復元できません。継続タスク以外では空です。
	OriginCommand TaskCommand `json:"origin_command,omitempty"`
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
	// SectionIndex は対象のセクション配列インデックス（0始まり）です
	// （short_video_from_section = 動画化対象 / regenerate_section_keyframes = 再生成対象）。
	// セクション名はサビ等で重複しうるため、名前ではなくインデックスで指定します。
	SectionIndex *int `json:"section_index,omitempty"`
	// VeoModel が空でないとき、動画生成に使う Veo モデルをタスク単位で差し替えます。
	VeoModel string `json:"veo_model,omitempty"`
	// VeoAspectRatio が空でないとき、アスペクト比（"16:9" または "9:16"）をタスク単位で指定します。
	// 新規レシピ作成（video_recipe_create/compose系）ではキーフレーム生成に使われ、
	// その値が VideoRecipe.AspectRatio に記録されます。既存ジョブに対する操作
	// （動画生成・カット再生成）では、ハンドラが記録済みの VideoRecipe.AspectRatio を
	// そのままここへ設定し直すため、キーフレームと動画のアスペクト比が常に一致します。
	VeoAspectRatio string `json:"veo_aspect_ratio,omitempty"`
	// OverwriteKeyframe が true のとき、再生成したキーフレームでレシピを上書きします
	// （regenerate_cut_keyframe / regenerate_section_keyframes コマンド専用）。
	OverwriteKeyframe bool `json:"overwrite_keyframe,omitempty"`
	// OriginalJobID は、再生成タスクの結果が実際に書き込まれる元ジョブのIDです
	// （regenerate_cut_keyframe / regenerate_section_keyframes / regenerate_zip コマンド専用）。
	// JobID は新規生成されたタスク自身のIDのため、通知等で参照先の History Detail を示すにはこちらを使います。
	OriginalJobID string `json:"original_job_id,omitempty"`
	// VisualAnchorOverride が空でないとき、再生成対象カットのビジュアルアンカー（プロンプト文言）をこの値に差し替えます（regenerate_cut_keyframe コマンド専用）。
	// カットごとに異なる文言を持つ値のため、セクション一括再生成では受け付けません。
	// EditPrompt が指定されている場合はそちらが優先され、VisualAnchorOverride は無視されます。
	VisualAnchorOverride string `json:"visual_anchor_override,omitempty"`
	// EditPrompt が空でないとき、フル再生成ではなく既存キーフレーム画像を編集する「編集モード」になります
	// （regenerate_cut_keyframe / regenerate_section_keyframes コマンド専用）。構図・ポーズ・背景は保ったまま、
	// この指示内容だけを反映します。セクション対象の場合は同じ指示を各カットへ順に適用します。
	EditPrompt string `json:"edit_prompt,omitempty"`
	// SeedOverride が非nilのとき、再生成に使うキャラクターシードを一時的にこの値へ差し替えます
	// （regenerate_cut_keyframe / regenerate_section_keyframes コマンド専用）。
	SeedOverride *int64 `json:"seed_override,omitempty"`
	// SeedOverrideCharacterID は、SeedOverride を適用する対象キャラクターIDです。
	// ハンドラーが再生成対象カットの既存 CharacterID を解決して設定します
	// （regenerate_cut_keyframe / regenerate_section_keyframes コマンド専用）。
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
		// 生成を伴わないため、必要なのは仕上げ対象のレシピだけです。
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

// validateRecipeSourceTask は、保存済みレシピを入力に取るコマンド群
// （mv_from_keyframe_video_recipe / 継続 / 再生成系 / ショート）に共通の三連
// 「recipe ∨ video_recipe ∨ recipe_url が必要 → recipe_url は GCS URI →
// aspect_ratio は許可値」を検証します。以前は 5 つの case がこの並びを丸ごと
// 繰り返しており、コマンド追加のたびに 1 箇所だけ検証が抜ける余地がありました。
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

// AllowedAspectRatios は Veo 生成で受け付けるアスペクト比の唯一の定義です。
// config のバリデーションとタスク検証がこれを共有します（compose.html の選択肢も
// この値と一致させてください — テンプレートは定数を参照できないため、
// TestComposeTemplateOffersAllAspectRatios が同期を検証します）。
var AllowedAspectRatios = []string{"16:9", "9:16"}

// DefaultAspectRatio は、タスクにも設定にも指定が無いときに使うアスペクト比です。
//
// go-veo-orchestrator は画作りの既定値を持たなくなったため（キットが既定を持つと
// VEO_ASPECT_RATIO と出所が二重になり、片方だけ変えたときに黙って食い違うため）、
// 既定はアプリ側のこの1箇所で決めます。
const DefaultAspectRatio = "16:9"

// AllowedImageSizes はキーフレーム画像の出力解像度として受け付ける値の唯一の定義です。
// AllowedAspectRatios と同じく、go-veo-orchestrator が既定値を持たなくなったため
// アプリ側がこの語彙を持ちます。
var AllowedImageSizes = []string{"1K", "2K", "4K"}

// IsAllowedImageSize は value が許可された解像度かを返します。
func IsAllowedImageSize(value string) bool {
	return slices.Contains(AllowedImageSizes, value)
}

// IsAllowedAspectRatio は value が許可されたアスペクト比かを返します。
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
