package ports

// VideoRecipe は ScriptRunner が生成する動画台本全体の構造です。
// Music Recipe と各カットの Audio Cue / Visual Anchor を同じ JSON に保持し、
// Veo への音楽同期プロンプトと後段の決定論的な結合処理の入力にします。
type VideoRecipe struct {
	ProjectTitle string      `json:"project_title"`
	Title        string      `json:"title,omitempty"`
	Theme        string      `json:"theme,omitempty"`
	Mood         string      `json:"mood,omitempty"`
	Tempo        int         `json:"tempo,omitempty"`
	Instruments  []string    `json:"instruments,omitempty"`
	Sections     []Section   `json:"sections,omitempty"`
	Lyrics       *Lyrics     `json:"lyrics,omitempty"`
	AudioModel   string      `json:"audio_model,omitempty"`
	ComposeMode  string      `json:"compose_mode,omitempty"`
	Seed         int64       `json:"seed,omitempty"`
	Description  string      `json:"description,omitempty"`
	MusicRecipe  MusicRecipe `json:"music_recipe"`
	Cuts         []Cut       `json:"cuts"`
}

// MusicRecipe は BGM 全体のテンポ・尺・スタイルを表す楽曲構成書です。
type MusicRecipe struct {
	TempoBPM         int     `json:"tempo_bpm"`
	TotalDurationSec float64 `json:"total_duration_sec"`
	Style            string  `json:"style"`
}

// Section は楽曲生成JSON内の可変長セクションです。
// Verse / Chorus などの曲展開を、そのまま動画タイムラインの候補カットとして扱えます。
type Section struct {
	Name            string  `json:"name"`
	DurationSeconds float64 `json:"duration_seconds"`
	Prompt          string  `json:"prompt"`
}

// Lyrics は楽曲生成JSON内の歌詞・テーマ情報です。
type Lyrics struct {
	Title     string   `json:"title"`
	Theme     string   `json:"theme"`
	Hook      string   `json:"hook"`
	Lyrics    string   `json:"lyrics"`
	Keywords  []string `json:"keywords"`
	Mood      string   `json:"mood"`
	Narrative string   `json:"narrative"`
}

// Cut は動画内の1カットを表します。
// audio_cue は BGM 上の展開、visual_anchor は映像上の固定指示です。
type Cut struct {
	CutIndex          int       `json:"cut_index"`
	DurationSec       float64   `json:"duration_sec"`
	AudioCue          string    `json:"audio_cue"`
	AudioReference    string    `json:"audio_reference,omitempty"`
	VisualAnchor      string    `json:"visual_anchor"`
	CharacterID       string    `json:"character_id"`
	Dialogue          string    `json:"dialogue,omitempty"`
	KeyframeReference string    `json:"keyframe_reference,omitempty"`
	VideoURL          string    `json:"video_url,omitempty"`
	VideoID           string    `json:"video_id,omitempty"`
	Status            CutStatus `json:"status,omitempty"`
	StartSec          float64   `json:"start_sec,omitempty"`
	EndSec            float64   `json:"end_sec,omitempty"`
}

// Cuts は Cut のスライスに対するカスタム型です。
type Cuts []Cut

type CutStatus string

const (
	CutStatusPending   CutStatus = "pending"
	CutStatusGenerated CutStatus = "generated"
	CutStatusFailed    CutStatus = "failed"
)

// Normalize はトップレベルの楽曲JSONと動画生成用JSONの差を吸収し、
// section 由来のカット生成とタイムライン補完を行います。
func (vr *VideoRecipe) Normalize() {
	if vr == nil {
		return
	}
	if vr.ProjectTitle == "" {
		vr.ProjectTitle = vr.Title
	}
	if vr.Title == "" {
		vr.Title = vr.ProjectTitle
	}
	if vr.MusicRecipe.TempoBPM == 0 {
		vr.MusicRecipe.TempoBPM = vr.Tempo
	}
	if vr.Tempo == 0 {
		vr.Tempo = vr.MusicRecipe.TempoBPM
	}
	if vr.MusicRecipe.Style == "" {
		vr.MusicRecipe.Style = vr.Mood
	}
	if vr.Mood == "" {
		vr.Mood = vr.MusicRecipe.Style
	}
	if len(vr.Cuts) == 0 && len(vr.Sections) > 0 {
		vr.Cuts = cutsFromSections(vr.Sections)
	}

	var current float64
	for i := range vr.Cuts {
		vr.Cuts[i].Normalize(i+1, current)
		current = vr.Cuts[i].EndSec
	}
	if vr.MusicRecipe.TotalDurationSec == 0 {
		vr.MusicRecipe.TotalDurationSec = current
	}
}

func cutsFromSections(sections []Section) []Cut {
	cuts := make([]Cut, 0, len(sections))
	for i, section := range sections {
		cuts = append(cuts, Cut{
			CutIndex:     i + 1,
			DurationSec:  section.DurationSeconds,
			AudioCue:     section.Prompt,
			VisualAnchor: section.Name,
		})
	}
	return cuts
}

// Normalize はカット番号と時間範囲を補完します。
func (c *Cut) Normalize(index int, startSec float64) {
	if c.CutIndex == 0 {
		c.CutIndex = index
	}
	if c.Status == "" {
		c.Status = CutStatusPending
	}
	if c.StartSec == 0 {
		c.StartSec = startSec
	}
	if c.EndSec == 0 {
		c.EndSec = c.StartSec + c.DurationSec
	}
}

func (c Cut) IsGenerated() bool {
	return c.Status == CutStatusGenerated || (c.VideoID != "" && c.VideoURL != "")
}
