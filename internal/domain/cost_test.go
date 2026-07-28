package domain

import (
	"math"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
)

func TestVeoPricingRateForFallsBackThroughTable(t *testing.T) {
	pricing := VeoPricing{"veo-a": 1.5, "": 0.25, "veo-zero": 0}

	if got := pricing.RateFor("veo-a"); got != 1.5 {
		t.Errorf("RateFor(veo-a) = %v, want 1.5", got)
	}
	if got := pricing.RateFor(" veo-a "); got != 1.5 {
		t.Errorf("RateFor with surrounding spaces = %v, want 1.5", got)
	}
	if got := pricing.RateFor("veo-unknown"); got != 0.25 {
		t.Errorf("RateFor(unknown) = %v, want the \"\" fallback 0.25", got)
	}
	// 0 は「設定されていない」と同じ扱いにする。0円のモデルは存在しないので、書き間違いを
	// 単価0として通すより、次の候補へ進むほうが安全。
	if got := pricing.RateFor("veo-zero"); got != 0.25 {
		t.Errorf("RateFor(zero rate) = %v, want the \"\" fallback 0.25", got)
	}
	if got := (VeoPricing{}).RateFor("veo-a"); got != DefaultVeoPriceUSDPerSecond {
		t.Errorf("RateFor on empty table = %v, want %v", got, DefaultVeoPriceUSDPerSecond)
	}
	// nil マップでもパニックせず既定値へ落ちること（VeoPricing 未設定のハンドラー経路）。
	if got := VeoPricing(nil).RateFor("veo-a"); got != DefaultVeoPriceUSDPerSecond {
		t.Errorf("RateFor on nil table = %v, want %v", got, DefaultVeoPriceUSDPerSecond)
	}
}

func TestGeneratedSecondsOfCutsCountsOnlyGeneratedCuts(t *testing.T) {
	cuts := []VideoCut{
		{
			AudioSync:   orchestrator.AudioSync{DurationSec: 8},
			VideoResult: orchestrator.VideoResult{Status: orchestrator.CutStatusGenerated},
		},
		{
			AudioSync:   orchestrator.AudioSync{DurationSec: 6},
			VideoResult: orchestrator.VideoResult{Status: orchestrator.CutStatusPending},
		},
		{
			AudioSync:   orchestrator.AudioSync{DurationSec: 4},
			VideoResult: orchestrator.VideoResult{Status: orchestrator.CutStatusFailed},
		},
		{
			// status 未設定でも video_id + video_url が揃っていれば生成済み扱い。
			AudioSync:   orchestrator.AudioSync{DurationSec: 7},
			VideoResult: orchestrator.VideoResult{VideoID: "v1", VideoURL: "gs://b/v1.mp4"},
		},
	}

	if got, want := GeneratedSecondsOfCuts(cuts), 15.0; got != want {
		t.Fatalf("GeneratedSecondsOfCuts() = %v, want %v (pending/failed cuts never reached Veo)", got, want)
	}
	if got := GeneratedSecondsOfCuts(nil); got != 0 {
		t.Fatalf("GeneratedSecondsOfCuts(nil) = %v, want 0", got)
	}
}

func TestVideoHistoryCutIsGenerated(t *testing.T) {
	tests := []struct {
		name string
		cut  VideoHistoryCut
		want bool
	}{
		{name: "status generated", cut: VideoHistoryCut{Status: CutStatusGenerated}, want: true},
		{name: "video url only", cut: VideoHistoryCut{VideoURL: "gs://b/v.mp4"}, want: true},
		{name: "blank video url", cut: VideoHistoryCut{VideoURL: "   "}, want: false},
		{name: "pending", cut: VideoHistoryCut{Status: "pending"}, want: false},
		{name: "zero value", cut: VideoHistoryCut{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cut.IsGenerated(); got != tt.want {
				t.Fatalf("IsGenerated() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyVeoCostEstimateFillsCutsAndTotal(t *testing.T) {
	detail := &VideoHistoryDetail{
		Cuts: []VideoHistoryCut{
			{CutIndex: 1, DurationSec: 8, Status: CutStatusGenerated},
			{CutIndex: 2, DurationSec: 6, Status: "pending"},
			{CutIndex: 3, DurationSec: 7, VideoURL: "gs://b/3.mp4"},
		},
	}

	ApplyVeoCostEstimate(detail, "veo-a", VeoPricing{"veo-a": 0.5})

	if got, want := detail.Cuts[0].EstimatedCostUSD, 4.0; got != want {
		t.Errorf("cut 1 cost = %v, want %v", got, want)
	}
	if got := detail.Cuts[1].EstimatedCostUSD; got != 0 {
		t.Errorf("pending cut cost = %v, want 0", got)
	}
	if got, want := detail.Cuts[2].EstimatedCostUSD, 3.5; got != want {
		t.Errorf("cut 3 cost = %v, want %v", got, want)
	}
	if got, want := detail.Cost.GeneratedSeconds, 15.0; got != want {
		t.Errorf("Cost.GeneratedSeconds = %v, want %v", got, want)
	}
	if got, want := detail.Cost.EstimatedUSD, 7.5; math.Abs(got-want) > 1e-9 {
		t.Errorf("Cost.EstimatedUSD = %v, want %v", got, want)
	}
	if detail.GeneratedSeconds != detail.Cost.GeneratedSeconds {
		t.Errorf("GeneratedSeconds = %v, want it to match Cost.GeneratedSeconds %v", detail.GeneratedSeconds, detail.Cost.GeneratedSeconds)
	}
	if detail.Cost.Model != "veo-a" || detail.Cost.RateUSDPerSecond != 0.5 {
		t.Errorf("Cost = %+v, want the resolved model and rate recorded for display", detail.Cost)
	}
}

// TestApplyVeoCostEstimateClearsStaleCutCost verifies a cut that is no longer generated (e.g. a
// section regeneration reset it to pending) doesn't keep a previously computed cost.
func TestApplyVeoCostEstimateClearsStaleCutCost(t *testing.T) {
	detail := &VideoHistoryDetail{
		Cuts: []VideoHistoryCut{{DurationSec: 8, Status: "pending", EstimatedCostUSD: 4}},
	}

	ApplyVeoCostEstimate(detail, "veo-a", VeoPricing{"veo-a": 0.5})

	if got := detail.Cuts[0].EstimatedCostUSD; got != 0 {
		t.Fatalf("EstimatedCostUSD = %v, want 0 for a cut that is no longer generated", got)
	}
	if detail.Cost.HasCost() {
		t.Fatalf("Cost.HasCost() = true, want false when nothing is generated")
	}
}

func TestApplyVeoCostEstimateIgnoresNilDetail(_ *testing.T) {
	// キーフレームのみのジョブや未設定リポジトリで nil が渡っても落ちないこと。
	// パニックしなければ成功なので、アサーションは無い。
	ApplyVeoCostEstimate(nil, "veo-a", VeoPricing{"veo-a": 0.5})
}

func TestApplyVeoCostEstimateToHistoriesUsesStoredSeconds(t *testing.T) {
	histories := []VideoHistory{
		{JobID: "a", GeneratedSeconds: 20},
		{JobID: "b"},
	}

	ApplyVeoCostEstimateToHistories(histories, "veo-a", VeoPricing{"veo-a": 0.5})

	if got, want := histories[0].Cost.EstimatedUSD, 10.0; got != want {
		t.Errorf("histories[0].Cost.EstimatedUSD = %v, want %v", got, want)
	}
	if !histories[0].Cost.HasCost() {
		t.Error("histories[0].Cost.HasCost() = false, want true")
	}
	if histories[1].Cost.HasCost() {
		t.Error("histories[1].Cost.HasCost() = true, want false for a keyframe-only job")
	}
}
