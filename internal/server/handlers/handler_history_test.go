package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/domain"
)

// TestHistoryDetailRendersKeyframeImage verifies history detail shows cut keyframes.
func TestHistoryDetailRendersKeyframeImage(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{
				JobID:    "video-recipe-20260618-081931-abc",
				Title:    "軌跡のアーキテクト",
				CutCount: 1,
			},
			Cuts: []domain.VideoHistoryCut{
				{
					CutIndex:          1,
					VisualAnchor:      "blue stage",
					KeyframeReference: "gs://bucket/keyframe.png",
					KeyframeURL:       "https://signed.example/keyframe.png",
					Status:            "pending",
				},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history/video-recipe-20260618-081931-abc", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "video-recipe-20260618-081931-abc")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"軌跡のアーキテクト",
		`src="https://signed.example/keyframe.png"`,
		"blue stage",
		"Cut 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("HistoryDetail body missing %q: %s", want, body)
		}
	}
	// 見出し文言ではなくプレイヤーそのもので判定する。「完成動画」という語は結合フォームの
	// 説明文にも出るため、文言で見るとプレイヤーと無関係な文章の追加だけで落ちる。
	if strings.Contains(body, "<video") {
		t.Fatalf("HistoryDetail body unexpectedly rendered the final-video player without FinalVideoSignedURL: %s", body)
	}
}

// TestHistoryDetailAppliesAspectRatioClass verifies that a 9:16 job's cut thumbnails and the
// completed-video player get the vertical aspect-ratio treatment (CSS class / inline
// aspect-ratio), instead of always assuming the default 16:9 box — a 9:16 keyframe rendered
// into a forced 16:9 box gets cropped by object-fit: cover.
func TestHistoryDetailAppliesAspectRatioClass(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{
				JobID:               "job-1",
				Title:               "Test Short",
				AspectRatio:         "9:16",
				FinalVideoURL:       "gs://bucket/jobs/job-1/videos/final.mp4",
				FinalVideoSignedURL: "https://signed.example/final.mp4",
			},
			Cuts: []domain.VideoHistoryCut{
				{CutIndex: 1, KeyframeURL: "https://signed.example/cut1.png"},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history/job-1", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="card-img-top history-keyframe history-keyframe--9x16"`,
		"aspect-ratio: 9 / 16",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("HistoryDetail body missing %q for a 9:16 job: %s", want, body)
		}
	}
	if strings.Contains(body, "aspect-ratio: 16 / 9") {
		t.Fatalf("HistoryDetail body unexpectedly used the default 16:9 aspect ratio for a 9:16 job: %s", body)
	}
}

// TestHistoryDetailRendersFinalVideo verifies the history detail page shows the chain-finalize
// result (final_video_url) as a prominent player, distinct from the per-cut cards.
func TestHistoryDetailRendersFinalVideo(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{
				JobID:               "job-1",
				Title:               "Test MV",
				FinalVideoURL:       "gs://bucket/jobs/job-1/videos/final.mp4",
				FinalVideoSignedURL: "https://signed.example/final.mp4",
			},
			Cuts: []domain.VideoHistoryCut{
				{CutIndex: 1, VideoSignedURL: "https://signed.example/cut1.mp4"},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history/job-1", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"完成動画",
		`src="https://signed.example/final.mp4"`,
		"Copy MV Job ID",
		// コピー対象は data 属性で渡す。JS の引数にテンプレート値を埋めていた頃は、
		// JS 文字列リテラル文脈のエスケープ（"/" → "\/" など）が絡んでいた。
		`data-copy-text="job-1"`,
		"カット別詳細",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("HistoryDetail body missing %q: %s", want, body)
		}
	}
}

// TestHistoryDetailRendersVeoCostEstimate verifies the detail page shows the job total and the
// per-cut cost next to each duration, and that a cut that never reached Veo is not priced.
// The rate is resolved at render time from the configured price table, so this also pins the
// wiring between Handler.VeoPricing and the template.
func TestHistoryDetailRendersVeoCostEstimate(t *testing.T) {
	h, err := NewHandlerWithOptions(assets.Templates, nil, ModelOptions{
		VeoModels:       []string{"veo-test"},
		DefaultVeoModel: "veo-test",
	}, CharacterOptions{})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions() error = %v", err)
	}
	h.VeoPricing = domain.VeoPricing{"veo-test": 0.50}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: "job-1", Title: "Cost MV", CutCount: 2},
			Cuts: []domain.VideoHistoryCut{
				{CutIndex: 1, DurationSec: 8, Status: domain.CutStatusGenerated},
				{CutIndex: 2, DurationSec: 6, Status: "pending"},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history/job-1", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Veo 概算コスト",
		"$4.00",     // 8sec × $0.50 — 生成済みカットのみ
		"8.0 sec",   // ジョブ合計の内訳
		"$0.50/sec", // 適用単価
		"veo-test",  // 単価の解決に使ったモデル
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("HistoryDetail body missing %q: %s", want, body)
		}
	}
	// 未生成カットは Veo を呼んでいないので、6sec × $0.50 = $3.00 は出てはいけない。
	if strings.Contains(body, "$3.00") {
		t.Fatalf("HistoryDetail priced a pending cut: %s", body)
	}
}

// TestHistoryDetailOmitsCostForKeyframeOnlyJob verifies the cost block disappears entirely when
// no cut has been generated, rather than showing a misleading $0.00 total.
func TestHistoryDetailOmitsCostForKeyframeOnlyJob(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: "job-1", Title: "Keyframes only"},
			Cuts:         []domain.VideoHistoryCut{{CutIndex: 1, DurationSec: 8, Status: "pending"}},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history/job-1", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Veo 概算コスト") {
		t.Fatalf("HistoryDetail rendered a cost block for a keyframe-only job: %s", rec.Body.String())
	}
}

// TestHistoryListRendersVeoCostColumn verifies the list page prices each row from the
// GeneratedSeconds the repository computed, and shows "-" for keyframe-only jobs.
func TestHistoryListRendersVeoCostColumn(t *testing.T) {
	h, err := NewHandlerWithOptions(assets.Templates, nil, ModelOptions{
		VeoModels:       []string{"veo-test"},
		DefaultVeoModel: "veo-test",
	}, CharacterOptions{})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions() error = %v", err)
	}
	h.VeoPricing = domain.VeoPricing{"veo-test": 0.50}
	h.HistoryRepository = fakeHistoryRepository{
		page: domain.VideoHistoryPage{
			Items: []domain.VideoHistory{
				{JobID: "job-1", Title: "Generated MV", GeneratedSeconds: 24, Generated: true},
				{JobID: "job-2", Title: "Keyframes only"},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history", nil)
	rec := httptest.NewRecorder()

	h.History(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("History status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Veo 概算", "$12.00", "24 sec"} {
		if !strings.Contains(body, want) {
			t.Fatalf("History body missing %q: %s", want, body)
		}
	}
}

// TestHistoryDetailRendersRecordedUsage verifies the detail page shows what was actually
// submitted to Veo alongside the finished runtime, and flags the regenerated cut. The finished
// runtime alone cannot reveal that cut 2 was billed twice — that is the whole point of the tally.
func TestHistoryDetailRendersRecordedUsage(t *testing.T) {
	h, err := NewHandlerWithOptions(assets.Templates, nil, ModelOptions{
		VeoModels:       []string{"veo-test"},
		DefaultVeoModel: "veo-test",
	}, CharacterOptions{})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions() error = %v", err)
	}
	h.VeoPricing = domain.VeoPricing{"veo-test": 0.50}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: "job-1", Title: "Reburned MV", CutCount: 2},
			Cuts: []domain.VideoHistoryCut{
				{CutIndex: 1, DurationSec: 8, Status: domain.CutStatusGenerated},
				{CutIndex: 2, DurationSec: 8, Status: domain.CutStatusGenerated},
			},
		},
		usage: &domain.VeoUsage{
			Model:            "veo-test",
			Calls:            3,
			SubmittedSeconds: 24,
			Cuts: []domain.VeoCutUsage{
				{CutIndex: 1, Calls: 1, SubmittedSeconds: 8},
				{CutIndex: 2, Calls: 2, SubmittedSeconds: 16},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history/job-1", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"完成品",
		"$8.00", // 16sec 完成
		"実投入",
		"$12.00", // 24sec 投入
		"3 回",
		"再生成ロス",
		"$4.00", // 差の 8sec
		"×2 生成",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("HistoryDetail body missing %q: %s", want, body)
		}
	}
}

// TestHistoryDetailWithoutUsageSaysSo verifies a job with no tally states that the regenerated
// portion is unknown, rather than silently reading as "no waste".
func TestHistoryDetailWithoutUsageSaysSo(t *testing.T) {
	h, err := NewHandlerWithOptions(assets.Templates, nil, ModelOptions{
		VeoModels:       []string{"veo-test"},
		DefaultVeoModel: "veo-test",
	}, CharacterOptions{})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions() error = %v", err)
	}
	h.VeoPricing = domain.VeoPricing{"veo-test": 0.50}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: "job-1", Title: "Old MV", CutCount: 1},
			Cuts:         []domain.VideoHistoryCut{{CutIndex: 1, DurationSec: 8, Status: domain.CutStatusGenerated}},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history/job-1", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "実投入") {
		t.Fatalf("HistoryDetail showed submitted seconds without a tally: %s", body)
	}
	if !strings.Contains(body, "実績記録が無いため") {
		t.Fatalf("HistoryDetail did not explain the missing tally: %s", body)
	}
}

// TestHistoryDetailSurvivesUsageReadFailure verifies a broken tally degrades to the estimate
// instead of failing the page.
func TestHistoryDetailSurvivesUsageReadFailure(t *testing.T) {
	h, err := NewHandlerWithOptions(assets.Templates, nil, ModelOptions{
		VeoModels:       []string{"veo-test"},
		DefaultVeoModel: "veo-test",
	}, CharacterOptions{})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions() error = %v", err)
	}
	h.VeoPricing = domain.VeoPricing{"veo-test": 0.50}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: "job-1", Title: "MV", CutCount: 1},
			Cuts:         []domain.VideoHistoryCut{{CutIndex: 1, DurationSec: 8, Status: domain.CutStatusGenerated}},
		},
		usageErr: errors.New("storage unavailable"),
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history/job-1", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "$4.00") {
		t.Fatalf("HistoryDetail lost the recipe-derived estimate: %s", rec.Body.String())
	}
}

// TestHistoryDetailCutCardCarriesCSRFToken は、カットカード内の「動画を作り直す」フォームに
// CSRF トークンが埋まることを検証します。historyCutCard は dict 引数で $ が再束縛される
// サブテンプレートのため、$.CSRFToken と書くと常に空になり、ブラウザからの送信が
// 403 Invalid CSRF token で落ちていました（dict 経由で渡すのが正解）。
func TestHistoryDetailCutCardCarriesCSRFToken(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{
				JobID:    "video-recipe-20260618-081931-abc",
				Title:    "CSRF regression",
				CutCount: 1,
			},
			Cuts: []domain.VideoHistoryCut{
				{
					CutIndex: 1,
					VideoURL: "gs://bucket/videos/cut_1.mp4",
					Status:   "generated",
				},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history/video-recipe-20260618-081931-abc", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "video-recipe-20260618-081931-abc")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeContext)
	ctx = auth.WithCSRFToken(ctx, "csrf-test-token")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="csrf_token" value="csrf-test-token"`) {
		t.Fatalf("cut card regenerate-video form is missing the CSRF token: %s", body)
	}
	if strings.Contains(body, `name="csrf_token" value=""`) {
		t.Fatalf("an empty csrf_token input was rendered: %s", body)
	}
}

// TestHistoryDetailRendersProgressBadge は、進捗バッジが段階と分数で描画されることを
// 確認します。以前は Generated の真偽値だけだったため、動画をあと1本残すジョブと
// 何も焼いていないジョブが同じ "keyframes" と表示され、残作業が読めませんでした。
func TestHistoryDetailRendersProgressBadge(t *testing.T) {
	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	// 3カット中2カットが動画生成済み = videos 2/3
	cuts := []domain.VideoCut{}
	for i := range 3 {
		cut := domain.VideoCut{}
		cut.CutIndex = i + 1
		cut.KeyframeReference = "gs://bucket/k.png"
		if i < 2 {
			cut.Status = video.CutStatusGenerated
			cut.VideoURL = "gs://bucket/v.mp4"
			cut.VideoID = "gs://bucket/v.mp4"
		}
		cuts = append(cuts, cut)
	}
	progress := domain.NewJobProgress(cuts)

	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{
				JobID:    "video-recipe-20260618-081931-abc",
				Title:    "進捗テスト",
				CutCount: 3,
				Progress: progress,
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/web/history/video-recipe-20260618-081931-abc", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", "video-recipe-20260618-081931-abc")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "videos 2/3") {
		t.Errorf("body should show the videos 2/3 progress, got: %s", body)
	}
	if strings.Contains(body, ">keyframes<") {
		t.Error("body still renders the old two-state keyframes badge")
	}
}
