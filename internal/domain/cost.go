package domain

import "strings"

// DefaultVeoPriceUSDPerSecond is the Veo output price (USD per generated second) used when no
// per-model price is configured.
//
// Veo bills by the seconds of generated video, so summing the cuts' duration_sec gives an
// estimate without calling any API. The default here is only a guide, though: the actual price
// varies with the model, whether audio is generated (VEO_GENERATE_AUDIO) and the contract. In
// operation, match the actual price list with VEO_PRICE_USD_PER_SEC.
//
// This estimate is not guaranteed to match Google's bill. It is for relative comparison between
// jobs and for spotting waste from regeneration.
const DefaultVeoPriceUSDPerSecond = 0.40

// VeoPricing is the output price per Veo model name (USD per generated second).
// The key "" is the fallback price applied to models not in the table.
type VeoPricing map[string]float64

// RateFor returns the price for the model name. When the model is not in the table it returns
// the "" key's fallback price, and failing that DefaultVeoPriceUSDPerSecond.
// A price of 0 or less is treated as "unset" and moves on to the next candidate.
func (p VeoPricing) RateFor(model string) float64 {
	if rate, ok := p[strings.TrimSpace(model)]; ok && rate > 0 {
		return rate
	}
	if rate, ok := p[""]; ok && rate > 0 {
		return rate
	}
	return DefaultVeoPriceUSDPerSecond
}

// VideoCostEstimate is an estimate of one job's Veo billing.
//
// It holds two durations side by side. GeneratedSeconds is "the finished length", computable
// from the recipe alone, so it is always filled. SubmittedSeconds is "the length actually sent to
// Veo", filled only when the veo_usage.json recorded at generation exists. Re-rendering the same
// cut (Cloud Tasks redelivery and the like) grows only the latter, so the difference (ExcessSeconds) is what regeneration threw away.
type VideoCostEstimate struct {
	// Model is the model name used to resolve the price. VideoRecipe does not store the Veo model
	// a job used, so the default model configured at display time is assumed. If a past job was
	// generated with another model, this price differs from the real one.
	Model string `json:"model,omitempty"`
	// RateUSDPerSecond is the price applied.
	RateUSDPerSecond float64 `json:"rate_usd_per_second,omitempty"`
	// GeneratedSeconds is the total duration of generated cuts.
	GeneratedSeconds float64 `json:"generated_seconds,omitempty"`
	// EstimatedUSD is GeneratedSeconds × RateUSDPerSecond.
	EstimatedUSD float64 `json:"estimated_usd,omitempty"`
	// SubmittedSeconds is the total duration actually sent to Veo. It is filled only for jobs with
	// a usage record (veo_usage.json).
	SubmittedSeconds float64 `json:"submitted_seconds,omitempty"`
	// SubmittedUSD is SubmittedSeconds × RateUSDPerSecond.
	SubmittedUSD float64 `json:"submitted_usd,omitempty"`
	// SubmittedCalls is the number of successful Veo generations. More than the cut count means re-renders.
	SubmittedCalls int `json:"submitted_calls,omitempty"`
	// HasUsage reports whether the usage record could be read. It distinguishes jobs with no record
	// (run before usage records existed) from jobs with zero usage.
	HasUsage bool `json:"has_usage,omitempty"`
}

// HasCost reports whether there is a cost worth showing. Keyframe-only jobs have no generated
// cuts, so it is false.
func (e VideoCostEstimate) HasCost() bool {
	return e.GeneratedSeconds > 0 || e.SubmittedSeconds > 0
}

// ExcessSeconds is the difference between the length actually sent and the finished length,
// i.e. what regeneration threw away. For jobs without a usage record it returns 0 (meaning
// "unknown", not "no waste"; tell them apart with HasUsage).
func (e VideoCostEstimate) ExcessSeconds() float64 {
	if !e.HasUsage {
		return 0
	}
	// Records can miss entries (see VeoUsage's comment), so usage can come out below the finished
	// length. A negative "waste" means nothing in that case, so round it to 0.
	if excess := e.SubmittedSeconds - e.GeneratedSeconds; excess > 0 {
		return excess
	}
	return 0
}

// ExcessUSD is ExcessSeconds multiplied by the price.
func (e VideoCostEstimate) ExcessUSD() float64 {
	return e.ExcessSeconds() * e.RateUSDPerSecond
}

// HasExcess reports whether there is a regeneration loss worth showing.
func (e VideoCostEstimate) HasExcess() bool {
	return e.ExcessSeconds() > 0
}

// GeneratedSecondsOfCuts returns the total duration of generated cuts.
// Ungenerated cuts never called Veo, so they are not added.
func GeneratedSecondsOfCuts(cuts []VideoCut) float64 {
	total := 0.0
	for _, cut := range cuts {
		if cut.IsGenerated() {
			total += cut.DurationSec
		}
	}
	return total
}

// EstimateVideoCost builds the estimated cost from the generated seconds and the model.
func EstimateVideoCost(seconds float64, model string, pricing VeoPricing) VideoCostEstimate {
	rate := pricing.RateFor(model)
	return VideoCostEstimate{
		Model:            strings.TrimSpace(model),
		RateUSDPerSecond: rate,
		GeneratedSeconds: seconds,
		EstimatedUSD:     seconds * rate,
	}
}

// ApplyVeoCostEstimate fills the per-cut and job-total estimated costs into the history detail.
// The price is resolved at display time rather than stored, so updating the price table makes
// past jobs' amounts follow immediately (stored metadata is not rewritten).
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

// ApplyVeoUsage overlays the recorded usage on the history detail. When usage is nil (a job run
// before usage records existed) it does nothing, so only the finished-length estimate remains.
//
// Call it after ApplyVeoCostEstimate. It uses the price the former resolved as-is.
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

// ApplyVeoCostEstimateToHistories fills the estimated cost into each item of the history listing.
// The listing holds only VideoHistory (it does not read the cut array), so it just multiplies
// the GeneratedSeconds computed when the recipe was read by the price.
func ApplyVeoCostEstimateToHistories(histories []VideoHistory, model string, pricing VeoPricing) {
	for i := range histories {
		histories[i].Cost = EstimateVideoCost(histories[i].GeneratedSeconds, model, pricing)
	}
}
