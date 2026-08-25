package server

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/shouni/gcp-kit/auth"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/builder"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/server/handlers"
)

// inlineTaskQueue is a no-op ports.TaskQueue stub for tests that only exercise routing/auth
// and never need a task to actually run.
type inlineTaskQueue struct{}

func (inlineTaskQueue) Enqueue(context.Context, *domain.Task) error { return nil }

func (inlineTaskQueue) EnqueueWithName(context.Context, string, *domain.Task) error { return nil }

var csrfInputPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// TestVideoRecipeCreatePostRequiresSessionCSRFToken verifies that video recipe creation POST requests require a session CSRF token.
func TestVideoRecipeCreatePostRequiresSessionCSRFToken(t *testing.T) {
	router, loginCookies := newAuthenticatedTestRouter(t)

	getReq := httptest.NewRequest(http.MethodGet, "/web/video-recipe-create", nil)
	for _, cookie := range loginCookies {
		getReq.AddCookie(cookie)
	}
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /web/video-recipe-create status = %d, want %d", getRec.Code, http.StatusOK)
	}
	matches := csrfInputPattern.FindStringSubmatch(getRec.Body.String())
	if len(matches) != 2 {
		t.Fatalf("GET /web/video-recipe-create did not render csrf token")
	}
	cookies := mergeCookies(loginCookies, getRec.Result().Cookies())
	if len(getRec.Result().Cookies()) == 0 {
		t.Fatalf("GET /web/video-recipe-create did not set csrf session cookie")
	}

	forbiddenReq := newVideoRecipeCreatePostRequest("")
	for _, cookie := range cookies {
		forbiddenReq.AddCookie(cookie)
	}
	forbiddenRec := httptest.NewRecorder()
	router.ServeHTTP(forbiddenRec, forbiddenReq)
	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("POST /web/video-recipe-create without csrf status = %d, want %d", forbiddenRec.Code, http.StatusForbidden)
	}

	acceptedReq := newVideoRecipeCreatePostRequest(matches[1])
	for _, cookie := range cookies {
		acceptedReq.AddCookie(cookie)
	}
	acceptedRec := httptest.NewRecorder()
	router.ServeHTTP(acceptedRec, acceptedReq)
	if acceptedRec.Code != http.StatusAccepted {
		t.Fatalf("POST /web/video-recipe-create with csrf status = %d, want %d; body=%s", acceptedRec.Code, http.StatusAccepted, acceptedRec.Body.String())
	}
}

// TestProtectedRoutesRedirectWhenUnauthenticated verifies protected routes redirect unauthenticated users.
func TestProtectedRoutesRedirectWhenUnauthenticated(t *testing.T) {
	router, _ := newAuthenticatedTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/web/video-recipe-create", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("GET /web/video-recipe-create status = %d, want %d", rec.Code, http.StatusFound)
	}
	if location := rec.Header().Get("Location"); !strings.HasPrefix(location, "/auth/login") {
		t.Fatalf("redirect location = %q, want /auth/login", location)
	}
}

// TestStaticRoutesServeOnlyStaticSubtree verifies static routing only serves the static subtree.
func TestStaticRoutesServeOnlyStaticSubtree(t *testing.T) {
	router, _ := newAuthenticatedTestRouter(t)

	cssReq := httptest.NewRequest(http.MethodGet, "/static/css/app.css", nil)
	cssRec := httptest.NewRecorder()
	router.ServeHTTP(cssRec, cssReq)
	if cssRec.Code != http.StatusOK {
		t.Fatalf("GET /static/css/app.css status = %d, want %d", cssRec.Code, http.StatusOK)
	}
	if !strings.Contains(cssRec.Body.String(), "--zunda-green") {
		t.Fatalf("GET /static/css/app.css body did not contain app css")
	}

	templateReq := httptest.NewRequest(http.MethodGet, "/static/templates/layout.html", nil)
	templateRec := httptest.NewRecorder()
	router.ServeHTTP(templateRec, templateReq)
	if templateRec.Code != http.StatusNotFound {
		t.Fatalf("GET /static/templates/layout.html status = %d, want %d", templateRec.Code, http.StatusNotFound)
	}
}

// TestNewRouterOmitsWorkerRouteForWebRole は、SERVER_ROLE=web のプロセスで
// /tasks/generate が登録されないことを確認します。
//
// 見るのは「401 で拒否される」ではなく「404 でルートが無い」ことです。役割を分けた
// 目的は公開サービス上からタスク受付口を消すことなので、アプリのコードが応答する
// 余地を残していない状態まで確かめる必要があります。
func TestNewRouterOmitsWorkerRouteForWebRole(t *testing.T) {
	// BuildHandlers が role=web で組む形: TaskAuth も Worker も nil。
	router, _ := newAuthenticatedTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/tasks/generate", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /tasks/generate status = %d, want %d (worker ルートは web ロールで登録されてはならない)", rec.Code, http.StatusNotFound)
	}
}

// TestNewRouterOmitsWebRoutesForWorkerRole は、SERVER_ROLE=worker のプロセスで
// Web 面と OAuth のルートが登録されないことを確認します。
func TestNewRouterOmitsWebRoutesForWorkerRole(t *testing.T) {
	// BuildHandlers が role=worker で組む形: Auth も Web も M2M も nil。
	router := newWorkerRoleTestRouter()

	for _, path := range []string{"/", "/web/history", "/auth/login"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want %d (web ルートは worker ロールで登録されてはならない)", path, rec.Code, http.StatusNotFound)
		}
	}
}

// TestNewRouterKeepsHealthForWorkerRole は、Worker 面だけの構成でもヘルスチェックが
// 残ることを確認します。Cloud Run の起動判定に使われます。
func TestNewRouterKeepsHealthForWorkerRole(t *testing.T) {
	router := newWorkerRoleTestRouter()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// newWorkerRoleTestRouter は SERVER_ROLE=worker 相当のルーターを返します。
func newWorkerRoleTestRouter() http.Handler {
	return NewRouter(&builder.AppHandlers{
		TaskAuth: auth.NewTaskVerifier("https://worker.example.test", []string{"tasks@example.iam.gserviceaccount.com"}),
	}, "")
}

// newAuthenticatedTestRouter creates a router and authenticated session cookies for tests.
func newAuthenticatedTestRouter(t *testing.T) (http.Handler, []*http.Cookie) {
	t.Helper()

	const (
		sessionName = "ap-mv-session"
		authKey     = "0123456789abcdef0123456789abcdef"
		encryptKey  = "0123456789abcdef0123456789abcdef"
		userEmail   = "user@example.com"
		taskAccount = "tasks@example.iam.gserviceaccount.com"
	)

	authHandler, err := auth.NewHandler(auth.Config{
		ClientID:          "client-id",
		ClientSecret:      "client-secret",
		RedirectURL:       "http://localhost:8080/auth/callback",
		SessionAuthKey:    authKey,
		SessionEncryptKey: encryptKey,
		SessionName:       sessionName,
		AllowedEmails:     []string{userEmail},
	})
	if err != nil {
		t.Fatalf("auth.NewHandler() error = %v", err)
	}

	webHandler, err := handlers.NewHandler(assets.Templates, inlineTaskQueue{})
	if err != nil {
		t.Fatalf("handlers.NewHandler() error = %v", err)
	}

	router := NewRouter(&builder.AppHandlers{Auth: authHandler, Web: webHandler}, "")
	return router, authenticatedSessionCookies(t, sessionName, []byte(authKey), []byte(encryptKey), userEmail)
}

// authenticatedSessionCookies creates signed session cookies for an authenticated test user.
func authenticatedSessionCookies(t *testing.T, sessionName string, authKey, encryptKey []byte, userEmail string) []*http.Cookie {
	t.Helper()

	store := sessions.NewCookieStore(authKey, encryptKey)
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   60 * 60 * 24 * 7,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	session, err := store.Get(req, sessionName)
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	session.Values[auth.DefaultUserSessionKey] = userEmail
	if err := session.Save(req, rec); err != nil {
		t.Fatalf("session.Save() error = %v", err)
	}
	return rec.Result().Cookies()
}

// mergeCookies merges cookies by name with overrides taking precedence.
func mergeCookies(base, overrides []*http.Cookie) []*http.Cookie {
	cookiesByName := make(map[string]*http.Cookie, len(base)+len(overrides))
	for _, cookie := range base {
		cookiesByName[cookie.Name] = cookie
	}
	for _, cookie := range overrides {
		cookiesByName[cookie.Name] = cookie
	}

	merged := make([]*http.Cookie, 0, len(cookiesByName))
	for _, cookie := range cookiesByName {
		merged = append(merged, cookie)
	}
	return merged
}

// newVideoRecipeCreatePostRequest creates a video recipe creation POST request for tests.
func newVideoRecipeCreatePostRequest(csrfToken string) *http.Request {
	form := url.Values{
		"text": {"test video recipe input"},
	}
	if csrfToken != "" {
		form.Set("csrf_token", csrfToken)
	}
	req := httptest.NewRequest(http.MethodPost, "/web/video-recipe-create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// 画面が指す /static/... が実際に配信できること。テンプレートの参照とディレクトリ名は
// vendor のバージョン更新のたびに両方を直す必要があり、片方だけ直すと 404 になります。
// ブラウザで開くまで気付けない種類の欠落なので、参照を実際に引いて確かめます。
func TestLayoutLocalAssetsAreServable(t *testing.T) {
	layout, err := assets.Templates.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatalf("layout.html を読めない: %v", err)
	}

	refs := regexp.MustCompile(`(?:href|src)="(/static/[^"]+)"`).FindAllStringSubmatch(string(layout), -1)
	if len(refs) == 0 {
		t.Fatal("layout.html に /static/ の参照が 1 つも無い（正規表現かテンプレートの変更を疑う）")
	}

	router, _ := newAuthenticatedTestRouter(t)
	for _, ref := range refs {
		target := ref[1]
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
			if rec.Code != http.StatusOK {
				t.Errorf("%s = %d, want 200", target, rec.Code)
			}
		})
	}
}

// 外部 CDN をやめて自前配信にした以上、layout.html から外部オリジンへの参照が
// 復活していないこと。復活すると CSP がそれを実行時に落とすので、CI で気付けるようにします。
func TestLayoutReferencesNoExternalOrigins(t *testing.T) {
	layout, err := assets.Templates.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatalf("layout.html を読めない: %v", err)
	}

	for _, ref := range regexp.MustCompile(`(?:href|src)="(https?://[^"]+)"`).FindAllStringSubmatch(string(layout), -1) {
		t.Errorf("外部オリジンへの参照が復活しています: %s", ref[1])
	}
}

// バージョン付きの vendor と、URL が変わらない自前アセットで Cache-Control を分けること。
func TestStaticCacheControlSeparatesVendorFromOwnAssets(t *testing.T) {
	router, _ := newAuthenticatedTestRouter(t)

	tests := []struct {
		target string
		want   string
	}{
		{"/static/vendor/bootstrap-5.3.8/bootstrap.min.css", vendorCacheControl},
		{"/static/css/app.css", ownAssetCacheControl},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.target, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("%s = %d, want 200", tt.target, rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.want {
				t.Errorf("Cache-Control = %q, want %q", got, tt.want)
			}
		})
	}
}

// CSP が全レスポンスに付き、script-src が緩められていないこと。
func TestResponsesCarryContentSecurityPolicy(t *testing.T) {
	router, _ := newAuthenticatedTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	policy := rec.Header().Get("Content-Security-Policy")
	if policy == "" {
		t.Fatal("Content-Security-Policy が付いていない")
	}
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("CSP に %q が無い: %s", want, policy)
		}
	}
	// script-src の 'unsafe-inline' はインラインスクリプト禁止（TestPagesLoadTheirScripts）
	// を無意味にします。style-src 側の 'unsafe-inline' は許容しているので、区間を限って見ます。
	scriptSrc := cspDirective(policy, "script-src")
	if scriptSrc == "" {
		t.Fatalf("script-src が無い: %s", policy)
	}
	if strings.Contains(scriptSrc, "unsafe-inline") || strings.Contains(scriptSrc, "unsafe-eval") {
		t.Errorf("script-src が緩められています: %s", scriptSrc)
	}
}

// キーフレームと動画は GCS の署名付き URL としてテンプレートへ直接埋まるため、
// img-src / media-src がそのホストを許していないと画面上で読み込みが落ちます。
func TestContentSecurityPolicyAllowsSignedMediaHost(t *testing.T) {
	router, _ := newAuthenticatedTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	policy := rec.Header().Get("Content-Security-Policy")

	for _, directive := range []string{"img-src", "media-src"} {
		if !strings.Contains(cspDirective(policy, directive), "https://storage.googleapis.com") {
			t.Errorf("%s が署名付き URL のホストを許していない: %s", directive, policy)
		}
	}
}

// cspDirective は CSP から 1 ディレクティブ分を取り出します。無ければ空文字を返します。
func cspDirective(policy, name string) string {
	for _, directive := range strings.Split(policy, ";") {
		directive = strings.TrimSpace(directive)
		if after, ok := strings.CutPrefix(directive, name+" "); ok {
			return after
		}
	}
	return ""
}

// 圧縮が効いていること。画面は日本語 UTF-8（1 文字 3 バイト）でよく縮むのに、
// これまで無圧縮で配信していました。
func TestCompressibleResponsesAreCompressed(t *testing.T) {
	router, _ := newAuthenticatedTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/static/vendor/bootstrap-5.3.8/bootstrap.min.css", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}

	reader, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer func() { _ = reader.Close() }()

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("解凍できない: %v", err)
	}
	if !strings.Contains(string(body), "Bootstrap") {
		t.Error("解凍した中身が Bootstrap の CSS でない")
	}
	if len(body) <= rec.Body.Len() {
		t.Errorf("圧縮後 %d バイトが元の %d バイトを下回っていない", rec.Body.Len(), len(body))
	}
}

// CSP 以外の防御ヘッダーも全レスポンスに付くこと。どれも 1 行で入る割に、
// 抜けても画面は正常に見えるため気付けません。
func TestResponsesCarrySecurityHeaders(t *testing.T) {
	router, _ := newAuthenticatedTestRouter(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "same-origin",
		"Strict-Transport-Security": hstsMaxAge,
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	// autoplay は塞ぎません（メディア再生が壊れます）。
	policy := rec.Header().Get("Permissions-Policy")
	if policy == "" {
		t.Error("Permissions-Policy が付いていない")
	}
	if strings.Contains(policy, "autoplay") {
		t.Errorf("Permissions-Policy が autoplay を塞いでいます: %s", policy)
	}
}
