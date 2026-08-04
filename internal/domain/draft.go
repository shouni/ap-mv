package domain

// VideoDraftFileName は下書きプレフィックス配下に保存される VideoRecipe のファイル名です。
// 完成ジョブの video_music_meta.json とは名前も置き場所も分けています。下書きは
// キーフレームを1枚も焼いていないため、履歴（video_music_meta.json を目印に走査される）
// に混ぜると「動画のできていないジョブ」として並んでしまいます。
const VideoDraftFileName = "video_recipe_draft.json"

// VideoDraft は下書き一覧に並べる 1 件分の表示用モデルです。
//
// VideoHistory と項目が似ていますが別の型にしています。下書きには署名付き URL も
// キーフレーム ZIP も完成動画も無く、VideoHistory の omitempty な項目を空のまま
// 使い回すと「まだ無い」のか「作られなかった」のかがテンプレートから区別できません。
type VideoDraft struct {
	JobID string `json:"job_id"`
	Title string `json:"title"`
	Mood  string `json:"mood,omitempty"`
	Tempo int    `json:"tempo,omitempty"`
	// CreatedAt はジョブ ID に埋め込まれた生成時刻の表示文字列です。
	CreatedAt string `json:"created_at,omitempty"`
	// VisualMode は MusicRecipe.ComposeMode（映像スタイル）です。
	VisualMode string `json:"visual_mode,omitempty"`
	// CutCount は SceneSplit 後に確定したカット数です。キーフレーム生成の課金単位が
	// これなので、下書きを進めるかどうかの判断に一番効きます。
	CutCount int `json:"cut_count,omitempty"`
	// TotalDurationSec は全カットの尺の合計です。曲尺と大きくズレていれば
	// カット割りの取り違えなので、キーフレームを焼く前に気づけます。
	TotalDurationSec float64 `json:"total_duration_sec,omitempty"`
	// SectionCount は MusicRecipe のセクション数です。
	SectionCount int `json:"section_count,omitempty"`
	// AspectRatio は下書き作成時に決まったアスペクト比です。
	AspectRatio string `json:"aspect_ratio,omitempty"`
	// StorageURI は下書き JSON の GCS URI です。ここから MV を生成するときは
	// この値をそのまま recipe_url として渡します。
	StorageURI string `json:"storage_uri,omitempty"`
}

// VideoDraftPage は下書き一覧の 1 ページ分です。
type VideoDraftPage struct {
	Items []VideoDraft `json:"items"`
	PageMeta
}

// TotalDurationSecOfCuts は全カットの尺の合計を返します。GeneratedSecondsOfCuts と違い
// 生成済みかどうかを問わないため、まだ 1 カットも生成していない下書きでも使えます。
func TotalDurationSecOfCuts(cuts []VideoCut) float64 {
	total := 0.0
	for _, cut := range cuts {
		total += cut.DurationSec
	}
	return total
}
