package ports

// Character は漫画に登場するキャラクターの定義を保持します。
type Character struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	VisualCues   []string `json:"visual_cues"`   // 生成プロンプトに注入する外見上の特徴
	ReferenceURL string   `json:"reference_url"` // 一貫性保持のための参照画像URL
	Seed         int64    `json:"seed"`          // DB保存等のために広い型を維持
	IsDefault    bool     `json:"is_default"`    // ページ全体の代表Seedとして優先するか
}

// Characters は表示順を持つリストとID検索用マップを保持します。
type Characters struct {
	List []Character
	ByID map[string]*Character
}
