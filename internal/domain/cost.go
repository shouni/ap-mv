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
// 2つの秒数を並べて持ちます。GeneratedSeconds は「完成品の尺」で、レシピだけから算出できる
// ため常に埋まります。SubmittedSeconds は「実際に Veo へ投げた尺」で、生成時に記録した
// veo_usage.json がある場合だけ埋まります。Cloud Tasks の再配信などで同じカットを焼き直すと
// 後者だけが増えるので、差（ExcessSeconds）が再生成で捨てた分になります。
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
	// SubmittedSeconds は実際に Veo へ投げた尺の合計です。実績記録（veo_usage.json）が
	// あるジョブでのみ埋まります。
	SubmittedSeconds float64 `json:"submitted_seconds,omitempty"`
	// SubmittedUSD は SubmittedSeconds × RateUSDPerSecond です。
	SubmittedUSD float64 `json:"submitted_usd,omitempty"`
	// SubmittedCalls は成功した Veo 生成の回数です。カット数より多ければ焼き直しがあります。
	SubmittedCalls int `json:"submitted_calls,omitempty"`
	// HasUsage は実績記録を読めたかを示します。記録が無い（実績記録の導入前に走った）
	// ジョブと、実績ゼロのジョブを区別するために持ちます。
	HasUsage bool `json:"has_usage,omitempty"`
}

// HasCost は、表示に値するコストがあるかを返します。キーフレームのみのジョブは
// 生成済みカットが無いため false になります。
func (e VideoCostEstimate) HasCost() bool {
	return e.GeneratedSeconds > 0 || e.SubmittedSeconds > 0
}

// ExcessSeconds は、実際に投げた尺と完成品の尺の差です。再生成で捨てた分にあたります。
// 実績記録が無いジョブでは 0 を返します（「無駄が無い」ではなく「分からない」を意味します。
// HasUsage で区別してください）。
func (e VideoCostEstimate) ExcessSeconds() float64 {
	if !e.HasUsage {
		return 0
	}
	// 記録は取りこぼしうる（VeoUsage のコメント参照）ので、実績が完成尺を下回ることがある。
	// その場合に負の「無駄」を表示しても意味が無いため 0 に丸める。
	if excess := e.SubmittedSeconds - e.GeneratedSeconds; excess > 0 {
		return excess
	}
	return 0
}

// ExcessUSD は ExcessSeconds に単価を掛けた金額です。
func (e VideoCostEstimate) ExcessUSD() float64 {
	return e.ExcessSeconds() * e.RateUSDPerSecond
}

// HasExcess は、表示に値する再生成ロスがあるかを返します。
func (e VideoCostEstimate) HasExcess() bool {
	return e.ExcessSeconds() > 0
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

// ApplyVeoUsage は、記録済みの実績を履歴詳細へ重ねます。usage が nil（実績記録の導入前に
// 走ったジョブ）のときは何もしないので、完成尺ベースの概算だけが残ります。
//
// ApplyVeoCostEstimate の後に呼んでください。単価は前者が解決した値をそのまま使います。
func ApplyVeoUsage(detail *VideoHistoryDetail, usage *VeoUsage) {
	if detail == nil || usage == nil {
		return
	}
	detail.Cost.HasUsage = true
	detail.Cost.SubmittedCalls = usage.Calls
	detail.Cost.SubmittedSeconds = usage.SubmittedSeconds
	detail.Cost.SubmittedUSD = usage.SubmittedSeconds * detail.Cost.RateUSDPerSecond
	for i := range detail.Cuts {
		detail.Cuts[i].GenerationCount = usage.CutCalls(detail.Cuts[i].CutIndex)
	}
}

// ApplyVeoCostEstimateToHistories は、履歴一覧の各項目に概算コストを埋めます。
// 一覧は VideoHistory しか持たない（カット配列を読まない）ため、レシピ読み込み時に
// 算出済みの GeneratedSeconds を単価に掛けるだけです。
func ApplyVeoCostEstimateToHistories(histories []VideoHistory, model string, pricing VeoPricing) {
	for i := range histories {
		histories[i].Cost = EstimateVideoCost(histories[i].GeneratedSeconds, model, pricing)
	}
}
