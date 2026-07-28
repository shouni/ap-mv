package domain

import "strings"

// DefaultVeoPriceUSDPerSecond は、モデル別の単価が設定されていないときに使う Veo 出力単価
// （USD / 生成1秒）です。
//
// Veo は生成した動画の秒数で課金されるため、カットの duration_sec を合計するだけで、API を
// 一切呼ばずに概算が出せます。ただしここの既定値はあくまで目安で、実際の単価はモデル・音声
// 生成の有無（VEO_GENERATE_AUDIO）・契約によって変わります。運用時は VEO_PRICE_USD_PER_SEC
// で実際の価格表に合わせてください。
//
// この概算が Google の請求額と一致することは保証しません。用途はジョブ間の相対比較と、
// 再生成による無駄の検出です。
const DefaultVeoPriceUSDPerSecond = 0.40

// VeoPricing は Veo モデル名ごとの出力単価（USD / 生成1秒）です。
// キー "" は、表に無いモデルへ適用するフォールバック単価として扱います。
type VeoPricing map[string]float64

// RateFor はモデル名に対応する単価を返します。モデルが表に無ければ "" キーの
// フォールバック単価、それも無ければ DefaultVeoPriceUSDPerSecond を返します。
// 0 以下の単価は「未設定」と同じ扱いにして、次の候補へ進みます。
func (p VeoPricing) RateFor(model string) float64 {
	if rate, ok := p[strings.TrimSpace(model)]; ok && rate > 0 {
		return rate
	}
	if rate, ok := p[""]; ok && rate > 0 {
		return rate
	}
	return DefaultVeoPriceUSDPerSecond
}

// VideoCostEstimate は、1ジョブぶんの Veo 課金量の概算です。
//
// GeneratedSeconds は「完成品の尺」であって「実際に Veo へ投げた秒数」ではありません。
// Cloud Tasks の再配信などで同じカットを焼き直しても、完成品の尺は変わらないためこの値は
// 増えません。実投入量との差＝再生成で捨てた分を出すには、生成のたびに実績を記録する必要が
// あります（未実装）。
type VideoCostEstimate struct {
	// Model は単価の解決に使ったモデル名です。VideoRecipe はジョブに使った Veo モデルを
	// 保存していないため、表示時点で設定されている既定モデルを当てています。過去ジョブを
	// 別モデルで生成していた場合、この単価は実際と異なります。
	Model string `json:"model,omitempty"`
	// RateUSDPerSecond は適用した単価です。
	RateUSDPerSecond float64 `json:"rate_usd_per_second,omitempty"`
	// GeneratedSeconds は生成済みカットの尺の合計です。
	GeneratedSeconds float64 `json:"generated_seconds,omitempty"`
	// EstimatedUSD は GeneratedSeconds × RateUSDPerSecond です。
	EstimatedUSD float64 `json:"estimated_usd,omitempty"`
}

// HasCost は、表示に値するコストがあるかを返します。キーフレームのみのジョブは
// 生成済みカットが無いため false になります。
func (e VideoCostEstimate) HasCost() bool {
	return e.GeneratedSeconds > 0
}

// GeneratedSecondsOfCuts は、生成済みカットの尺の合計を返します。
// 未生成のカットは Veo を呼んでいないため加算しません。
func GeneratedSecondsOfCuts(cuts []VideoCut) float64 {
	total := 0.0
	for _, cut := range cuts {
		if cut.IsGenerated() {
			total += cut.DurationSec
		}
	}
	return total
}

// EstimateVideoCost は、生成済み秒数とモデルから概算コストを組み立てます。
func EstimateVideoCost(seconds float64, model string, pricing VeoPricing) VideoCostEstimate {
	rate := pricing.RateFor(model)
	return VideoCostEstimate{
		Model:            strings.TrimSpace(model),
		RateUSDPerSecond: rate,
		GeneratedSeconds: seconds,
		EstimatedUSD:     seconds * rate,
	}
}

// ApplyVeoCostEstimate は、履歴詳細にカット別とジョブ合計の概算コストを埋めます。
// 単価はストレージに保存された値ではなく表示時に解決するため、価格表を更新すれば過去ジョブの
// 表示額もその場で追従します（保存済みメタデータは書き換えません）。
func ApplyVeoCostEstimate(detail *VideoHistoryDetail, model string, pricing VeoPricing) {
	if detail == nil {
		return
	}
	rate := pricing.RateFor(model)
	total := 0.0
	for i := range detail.Cuts {
		cut := &detail.Cuts[i]
		if !cut.IsGenerated() {
			cut.EstimatedCostUSD = 0
			continue
		}
		cut.EstimatedCostUSD = cut.DurationSec * rate
		total += cut.DurationSec
	}
	detail.Cost = EstimateVideoCost(total, model, pricing)
	detail.GeneratedSeconds = total
}

// ApplyVeoCostEstimateToHistories は、履歴一覧の各項目に概算コストを埋めます。
// 一覧は VideoHistory しか持たない（カット配列を読まない）ため、レシピ読み込み時に
// 算出済みの GeneratedSeconds を単価に掛けるだけです。
func ApplyVeoCostEstimateToHistories(histories []VideoHistory, model string, pricing VeoPricing) {
	for i := range histories {
		histories[i].Cost = EstimateVideoCost(histories[i].GeneratedSeconds, model, pricing)
	}
}
