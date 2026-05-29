package domain

type AIModels struct {
	// TextModel は歌詞生成およびレシピ構築（LLM）に使用するモデル
	TextModel string `json:"text_model,omitempty"`
	// ImageModel はジャケット画像生成に使用するモデル
	ImageModel string `json:"image_model,omitempty"`
	Seed       *int64 `json:"seed,omitempty"`
}
