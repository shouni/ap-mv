package domain

import (
	"math"
	"testing"
	"time"

	"github.com/shouni/go-veo-orchestrator/video"
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
			DurationSec: 8,
			Status:      video.CutStatusGenerated,
		},
		{
			DurationSec: 6,
			Status:      video.CutStatusPending,
		},
		{
			DurationSec: 4,
			Status:      video.CutStatusFailed,
		},
		{
			// status 未設定でも video_id + video_url が揃っていれば生成済み扱い。
			DurationSec: 7,
			VideoID:     "v1", VideoURL: "gs://b/v1.mp4",
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

func TestVeoUsageRecordAccumulatesPerCut(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	usage := &VeoUsage{}

	usage.Record(2, 8, "veo-a", now)
	usage.Record(1, 6, "", now.Add(time.Minute))
	// 同じカットの2回目 = 焼き直し。完成品の尺は変わらないが、課金は2回分発生している。
	usage.Record(2, 8, "veo-a", now.Add(2*time.Minute))

	if usage.Calls != 3 {
		t.Errorf("Calls = %d, want 3", usage.Calls)
	}
	if usage.SubmittedSeconds != 22 {
		t.Errorf("SubmittedSeconds = %v, want 22", usage.SubmittedSeconds)
	}
	if usage.SchemaVersion != VeoUsageSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", usage.SchemaVersion, VeoUsageSchemaVersion)
	}
	// モデル未指定の記録で、判明済みのモデル名が空へ潰されないこと。
	if usage.Model != "veo-a" {
		t.Errorf("Model = %q, want it preserved across a record with no model", usage.Model)
	}
	if got := usage.CutCalls(2); got != 2 {
		t.Errorf("CutCalls(2) = %d, want 2", got)
	}
	if got := usage.CutCalls(1); got != 1 {
		t.Errorf("CutCalls(1) = %d, want 1", got)
	}
	if got := usage.CutCalls(99); got != 0 {
		t.Errorf("CutCalls(unknown) = %d, want 0", got)
	}
	// カットは番号順に並べて保存する（人が veo_usage.json を直接読むため）。
	if len(usage.Cuts) != 2 || usage.Cuts[0].CutIndex != 1 || usage.Cuts[1].CutIndex != 2 {
		t.Fatalf("Cuts = %+v, want two entries sorted by cut index", usage.Cuts)
	}
	if usage.Cuts[1].SubmittedSeconds != 16 {
		t.Errorf("Cuts[1].SubmittedSeconds = %v, want 16 (two 8s generations)", usage.Cuts[1].SubmittedSeconds)
	}
	if !usage.Cuts[1].LastGeneratedAt.Equal(now.Add(2 * time.Minute)) {
		t.Errorf("Cuts[1].LastGeneratedAt = %v, want the latest generation time", usage.Cuts[1].LastGeneratedAt)
	}
}

func TestVeoUsageNilReceiverIsSafe(t *testing.T) {
	var usage *VeoUsage
	usage.Record(1, 8, "veo-a", time.Now())
	if got := usage.CutCalls(1); got != 0 {
		t.Fatalf("CutCalls on nil = %d, want 0", got)
	}
}

// TestApplyVeoUsageExposesRegenerationWaste is the point of the whole feature: the finished
// runtime alone cannot show that a cut was billed twice, so the recorded tally has to.
func TestApplyVeoUsageExposesRegenerationWaste(t *testing.T) {
	detail := &VideoHistoryDetail{
		Cuts: []VideoHistoryCut{
			{CutIndex: 1, DurationSec: 8, Status: CutStatusGenerated},
			{CutIndex: 2, DurationSec: 8, Status: CutStatusGenerated},
		},
	}
	ApplyVeoCostEstimate(detail, "veo-a", VeoPricing{"veo-a": 0.5})

	ApplyVeoUsage(detail, &VeoUsage{
		Calls:            3,
		SubmittedSeconds: 24,
		Cuts: []VeoCutUsage{
			{CutIndex: 1, Calls: 1, SubmittedSeconds: 8},
			{CutIndex: 2, Calls: 2, SubmittedSeconds: 16},
		},
	})

	if got, want := detail.Cost.EstimatedUSD, 8.0; got != want {
		t.Errorf("EstimatedUSD = %v, want %v (finished runtime is unchanged)", got, want)
	}
	if got, want := detail.Cost.SubmittedUSD, 12.0; got != want {
		t.Errorf("SubmittedUSD = %v, want %v", got, want)
	}
	if got, want := detail.Cost.ExcessSeconds(), 8.0; got != want {
		t.Errorf("ExcessSeconds() = %v, want %v", got, want)
	}
	if got, want := detail.Cost.ExcessUSD(), 4.0; got != want {
		t.Errorf("ExcessUSD() = %v, want %v", got, want)
	}
	if !detail.Cost.HasExcess() {
		t.Error("HasExcess() = false, want true")
	}
	if got := detail.Cuts[1].GenerationCount; got != 2 {
		t.Errorf("Cuts[1].GenerationCount = %d, want 2", got)
	}
	if detail.Cost.SubmittedCalls != 3 {
		t.Errorf("SubmittedCalls = %d, want 3", detail.Cost.SubmittedCalls)
	}
}

// TestApplyVeoUsageWithoutRecordLeavesEstimateAlone verifies jobs that predate the tally keep
// showing the recipe-derived estimate, and are not mistaken for jobs with zero waste.
func TestApplyVeoUsageWithoutRecordLeavesEstimateAlone(t *testing.T) {
	detail := &VideoHistoryDetail{
		Cuts: []VideoHistoryCut{{CutIndex: 1, DurationSec: 8, Status: CutStatusGenerated}},
	}
	ApplyVeoCostEstimate(detail, "veo-a", VeoPricing{"veo-a": 0.5})

	ApplyVeoUsage(detail, nil)

	if detail.Cost.HasUsage {
		t.Error("HasUsage = true, want false for a job with no recorded tally")
	}
	if detail.Cost.SubmittedSeconds != 0 {
		t.Errorf("SubmittedSeconds = %v, want 0", detail.Cost.SubmittedSeconds)
	}
	if got, want := detail.Cost.EstimatedUSD, 4.0; got != want {
		t.Errorf("EstimatedUSD = %v, want %v", got, want)
	}
	if detail.Cost.HasExcess() {
		t.Error("HasExcess() = true, want false — unknown waste must not read as measured waste")
	}
}

// TestExcessSecondsClampsUnderCount verifies a tally that lost an update (concurrent jobs) never
// renders as negative waste.
func TestExcessSecondsClampsUnderCount(t *testing.T) {
	cost := VideoCostEstimate{HasUsage: true, GeneratedSeconds: 16, SubmittedSeconds: 8, RateUSDPerSecond: 0.5}

	if got := cost.ExcessSeconds(); got != 0 {
		t.Fatalf("ExcessSeconds() = %v, want 0 when the tally under-counts", got)
	}
	if got := cost.ExcessUSD(); got != 0 {
		t.Fatalf("ExcessUSD() = %v, want 0", got)
	}
}
