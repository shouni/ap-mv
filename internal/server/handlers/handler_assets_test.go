package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/domain"
)

// スクリプトはテンプレートの外に置き、必要なページだけが読み込みます。
// インラインのままだと画面の構造と振る舞いが同じファイルに混ざり、
// テンプレート値を JS の文字列リテラルへ埋め込む書き方にもなっていました。
func TestPagesLoadTheirScripts(t *testing.T) {
	t.Parallel()

	body := renderHistoryDetailPage(t)
	if strings.Contains(body, "<script>") {
		t.Error("インラインスクリプトが残っています")
	}
	for _, attribute := range []string{"onclick=", "onsubmit=", "onchange="} {
		if strings.Contains(body, attribute) {
			t.Errorf("%s 属性が残っています", attribute)
		}
	}
	// 削除は fetch で送るため、フォームの外から読める CSRF トークンが要ります。
	if !strings.Contains(body, `id="csrf_token"`) {
		t.Error("CSRF トークンの hidden input がありません")
	}
	if !strings.Contains(body, `data-delete-url="/history/`) {
		t.Error("削除ボタンに data-delete-url がありません")
	}
}

// ナビのモデル名は設定から出します。固定文言だった頃は、モデルを入れ替えても
// 表示だけが古いモデルを指し続けました。
func TestNavShowsConfiguredGeminiModel(t *testing.T) {
	t.Parallel()

	if body := renderHistoryPage(t); !strings.Contains(body, "gemini-test-model") {
		t.Error("ナビに設定した Gemini モデルが出ていません")
	} else if strings.Contains(body, "Gemini 3 Flash") {
		t.Error("固定のモデル名が残っています")
	}
}

// renderHistoryPage は、フォームを持たないページの代表として履歴一覧を描きます。
// モデル選択肢を組み立てないページでも、ナビにはモデル名が出る必要があります。
func renderHistoryPage(t *testing.T) string {
	t.Helper()

	h, err := NewHandlerWithOptions(assets.Templates, nil,
		ModelOptions{
			GeminiModels:       []string{"gemini-test-model"},
			DefaultGeminiModel: "gemini-test-model",
		},
		CharacterOptions{},
	)
	if err != nil {
		t.Fatalf("NewHandlerWithOptions() error = %v", err)
	}
	h.HistoryRepository = fakeHistoryRepository{
		page: domain.VideoHistoryPage{Items: []domain.VideoHistory{{
			JobID: "recipe-20260618-081931-abcdef123456",
			Title: "スクリプト外出しの確認",
		}}},
	}

	rec := httptest.NewRecorder()
	h.History(rec, httptest.NewRequest(http.MethodGet, "/history", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("History status = %d; body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// renderHistoryDetailPage は、削除ボタンと CSRF トークンを持つページの代表として
// 履歴詳細を描きます。一覧からの削除は下書き専用の導線だったため、統合後は
// 詳細画面がこの検証の対象です。
func renderHistoryDetailPage(t *testing.T) string {
	t.Helper()

	h, err := NewHandler(assets.Templates, nil)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	const jobID = "recipe-20260618-081931-abcdef123456"
	h.HistoryRepository = fakeHistoryRepository{
		detail: domain.VideoHistoryDetail{
			VideoHistory: domain.VideoHistory{JobID: jobID, Title: "削除導線の確認"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/history/"+jobID, nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	h.HistoryDetail(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HistoryDetail status = %d; body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestNavMarksCurrentPage は、ナビが現在地を active + aria-current で示すことを確認します。
// 兄弟アプリ（ap-comp / ap-story / ap-voice）はいずれも持っていて ap-mv だけ無く、
// どの画面にいるかがナビから読めませんでした。
func TestNavMarksCurrentPage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		target   string
		wantHref string
	}{
		"履歴":     {target: "/history", wantHref: `href="/history"`},
		"台本のみ一覧": {target: "/history?stage=script", wantHref: `href="/history?stage=script"`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h, err := NewHandler(assets.Templates, nil)
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}
			h.HistoryRepository = fakeHistoryRepository{}

			rec := httptest.NewRecorder()
			h.History(rec, httptest.NewRequest(http.MethodGet, tt.target, nil))

			body := rec.Body.String()
			if !strings.Contains(body, `aria-current="page" `+tt.wantHref) {
				t.Errorf("%s should be marked as the current page; body=%s", tt.wantHref, body)
			}
			// active は 1 つだけ。両方が現在地になると、どこにいるか読めません。
			if got := strings.Count(body, `aria-current="page"`); got != 1 {
				t.Errorf("aria-current count = %d, want exactly 1", got)
			}
		})
	}
}

// TestNavLinksCarryIcons pins that every nav item has a Bootstrap icon, matching ap-story and
// ap-voice. The stylesheet was already loaded but only the brand and footer used it.
func TestNavLinksCarryIcons(t *testing.T) {
	t.Parallel()

	body := renderHistoryPage(t)
	for _, icon := range []string{"bi-house", "bi-film", "bi-file-earmark-text", "bi-clock-history"} {
		if !strings.Contains(body, icon) {
			t.Errorf("nav is missing the %s icon", icon)
		}
	}
}
