package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/shouni/gcp-kit/auth"

	"ap-mv/assets"
	"ap-mv/internal/builder"
	"ap-mv/internal/ports"
	"ap-mv/internal/server/handlers"
)

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

// newAuthenticatedTestRouter creates a router and authenticated session cookies for tests.
func newAuthenticatedTestRouter(t *testing.T) (http.Handler, []*http.Cookie) {
	t.Helper()

	const (
		sessionName = "ap-mv-session"
		authKey     = "0123456789abcdef0123456789abcdef"
		encryptKey  = "0123456789abcdef0123456789abcdef"
		userEmail   = "user@example.com"
	)

	authHandler, err := auth.NewHandler(auth.Config{
		ClientID:          "client-id",
		ClientSecret:      "client-secret",
		RedirectURL:       "http://localhost:8080/auth/callback",
		SessionAuthKey:    authKey,
		SessionEncryptKey: encryptKey,
		SessionName:       sessionName,
		AllowedEmails:     []string{userEmail},
		TaskAudienceURL:   "http://localhost:8080/tasks/generate",
	})
	if err != nil {
		t.Fatalf("auth.NewHandler() error = %v", err)
	}

	webHandler, err := handlers.NewHandler(assets.Templates, ports.InlineTaskQueue{})
	if err != nil {
		t.Fatalf("handlers.NewHandler() error = %v", err)
	}

	router := NewRouter(&builder.AppHandlers{Auth: authHandler, Web: webHandler, StaticFiles: assets.StaticFiles})
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
