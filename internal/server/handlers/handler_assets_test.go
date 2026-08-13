package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/domain"
)

// スクリプトはテンプレートの外に置き、必要なページだけが読み込みます。
// インラインのままだと画面の構造と振る舞いが同じファイルに混ざり、
// テンプレート値を JS の文字列リテラルへ埋め込む書き方にもなっていました。
func TestPagesLoadTheirScripts(t *testing.T) {
	t.Parallel()

	body := renderDraftsPage(t)
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
	if !strings.Contains(body, `data-delete-url="/web/drafts/`) {
		t.Error("削除ボタンに data-delete-url がありません")
	}
}

// ナビのモデル名は設定から出します。固定文言だった頃は、モデルを入れ替えても
// 表示だけが古いモデルを指し続けました。
func TestNavShowsConfiguredGeminiModel(t *testing.T) {
	t.Parallel()

	if body := renderDraftsPage(t); !strings.Contains(body, "gemini-test-model") {
		t.Error("ナビに設定した Gemini モデルが出ていません")
	} else if strings.Contains(body, "Gemini 3 Flash") {
		t.Error("固定のモデル名が残っています")
	}
}

// renderDraftsPage は、フォームを持たないページの代表として下書き一覧を描きます。
// モデル選択肢を組み立てないページでも、ナビにはモデル名が出る必要があります。
func renderDraftsPage(t *testing.T) string {
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
	h.DraftRepository = &fakeDraftRepository{
		page: domain.VideoDraftPage{Items: []domain.VideoDraft{{
			JobID: draftTestJobID,
			Title: "スクリプト外出しの確認",
		}}},
	}

	rec := httptest.NewRecorder()
	h.Drafts(rec, httptest.NewRequest(http.MethodGet, "/web/drafts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("Drafts status = %d; body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}
