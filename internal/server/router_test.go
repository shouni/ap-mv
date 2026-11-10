package server

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/shouni/gcp-kit/auth/oidc"
	"github.com/shouni/gcp-kit/auth/session"

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

// TestProtectedRoutesRedirectWhenUnauthenticated verifies protected routes redirect unauthenticated users.
func TestProtectedRoutesRedirectWhenUnauthenticated(t *testing.T) {
	router := newWebRoleTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/compose", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("GET /compose status = %d, want %d", rec.Code, http.StatusFound)
	}
	if location := rec.Header().Get("Location"); !strings.HasPrefix(location, "/auth/login") {
		t.Fatalf("redirect location = %q, want /auth/login", location)
	}
}

// TestStaticRoutesServeOnlyStaticSubtree verifies static routing only serves the static subtree.
func TestStaticRoutesServeOnlyStaticSubtree(t *testing.T) {
	router := newWebRoleTestRouter(t)

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

// TestNewRouterOmitsWorkerRouteForWebRole checks that /tasks/generate is not registered in
// a SERVER_ROLE=web process.
//
// What it looks at is not "rejected with 401" but "404, no route". The point of splitting roles
// is to remove the task intake from the public service, so it has to confirm that no room is
// left for the app's code to answer at all.
func TestNewRouterOmitsWorkerRouteForWebRole(t *testing.T) {
	// The shape BuildHandlers builds for role=web: TaskAuth and Worker are both nil.
	router := newWebRoleTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/tasks/generate", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /tasks/generate status = %d, want %d (the worker route must not be registered for the web role)", rec.Code, http.StatusNotFound)
	}
}

// TestNewRouterOmitsWebRoutesForWorkerRole checks that the web face and OAuth routes are not
// registered in a SERVER_ROLE=worker process.
func TestNewRouterOmitsWebRoutesForWorkerRole(t *testing.T) {
	// The shape BuildHandlers builds for role=worker: Auth, Web and M2M are all nil.
	router := newWorkerRoleTestRouter(t)

	for _, path := range []string{"/", "/jobs", "/auth/login"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want %d (web routes must not be registered for the worker role)", path, rec.Code, http.StatusNotFound)
		}
	}
}

// TestNewRouterKeepsHealthForWorkerRole checks that the health check remains even with only
// the worker face. Cloud Run uses it to decide the instance has started.
func TestNewRouterKeepsHealthForWorkerRole(t *testing.T) {
	router := newWorkerRoleTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// newWorkerRoleTestRouter returns a router equivalent to SERVER_ROLE=worker.
func newWorkerRoleTestRouter(t *testing.T) http.Handler {
	t.Helper()
	return NewRouter(&builder.AppHandlers{
		TaskAuth: mustOIDC(t, "https://worker.example.test", []string{"tasks@example.iam.gserviceaccount.com"}),
	}, "")
}

// newWebRoleTestRouter returns a router equivalent to SERVER_ROLE=web.
func newWebRoleTestRouter(t *testing.T) http.Handler {
	t.Helper()

	const (
		sessionName = "ap-mv-session"
		authKey     = "0123456789abcdef0123456789abcdef"
		encryptKey  = "0123456789abcdef0123456789abcdef"
		userEmail   = "user@example.com"
	)

	authHandler, err := session.New(session.Config{
		ClientID:      "client-id",
		ClientSecret:  "client-secret",
		ServiceURL:    "http://localhost:8080",
		Store:         session.NewMemoryStore(),
		SessionName:   sessionName,
		AllowedEmails: []string{userEmail},
	})
	if err != nil {
		t.Fatalf("auth.NewHandler() error = %v", err)
	}

	webHandler, err := handlers.NewHandler(assets.Templates, inlineTaskQueue{})
	if err != nil {
		t.Fatalf("handlers.NewHandler() error = %v", err)
	}

	return NewRouter(&builder.AppHandlers{Auth: authHandler, Web: webHandler}, "")
}

// The /static/... the pages point at can actually be served. The template references and the
// directory name both have to be fixed on every vendor version bump, and fixing only one gives
// 404. It is the kind of gap nobody notices until it is opened in a browser, so follow the references for real.
func TestLayoutLocalAssetsAreServable(t *testing.T) {
	layout, err := assets.Templates.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatalf("cannot read layout.html: %v", err)
	}

	refs := regexp.MustCompile(`(?:href|src)="(/static/[^"]+)"`).FindAllStringSubmatch(string(layout), -1)
	if len(refs) == 0 {
		t.Fatal("layout.html has no /static/ reference at all (suspect a change to the regexp or the template)")
	}

	router := newWebRoleTestRouter(t)
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

// Having dropped external CDNs for self-hosting, layout.html must not regain references to
// external origins. If one came back, the CSP would block it at run time, so catch it in CI.
func TestLayoutReferencesNoExternalOrigins(t *testing.T) {
	layout, err := assets.Templates.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatalf("cannot read layout.html: %v", err)
	}

	for _, ref := range regexp.MustCompile(`(?:href|src)="(https?://[^"]+)"`).FindAllStringSubmatch(string(layout), -1) {
		t.Errorf("a reference to an external origin is back: %s", ref[1])
	}
}

// The CSP is on every response and script-src has not been loosened.
func TestResponsesCarryContentSecurityPolicy(t *testing.T) {
	router := newWebRoleTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	policy := rec.Header().Get("Content-Security-Policy")
	if policy == "" {
		t.Fatal("Content-Security-Policy is missing")
	}
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("CSP lacks %q: %s", want, policy)
		}
	}
	// 'unsafe-inline' in script-src would make the inline-script ban (TestPagesLoadTheirScripts)
	// meaningless. 'unsafe-inline' on the style-src side is tolerated, so look only within the directive.
	scriptSrc := cspDirective(policy, "script-src")
	if scriptSrc == "" {
		t.Fatalf("script-src is missing: %s", policy)
	}
	if strings.Contains(scriptSrc, "unsafe-inline") || strings.Contains(scriptSrc, "unsafe-eval") {
		t.Errorf("script-src has been loosened: %s", scriptSrc)
	}
}

// Keyframes and videos are embedded in templates directly as GCS signed URLs, so unless
// img-src / media-src allow that host, loading fails on the page.
func TestContentSecurityPolicyAllowsSignedMediaHost(t *testing.T) {
	router := newWebRoleTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	policy := rec.Header().Get("Content-Security-Policy")

	for _, directive := range []string{"img-src", "media-src"} {
		if !strings.Contains(cspDirective(policy, directive), "https://storage.googleapis.com") {
			t.Errorf("%s does not allow the signed URL host: %s", directive, policy)
		}
	}
}

// cspDirective takes one directive out of the CSP. It returns an empty string when there is none.
func cspDirective(policy, name string) string {
	for directive := range strings.SplitSeq(policy, ";") {
		directive = strings.TrimSpace(directive)
		if after, ok := strings.CutPrefix(directive, name+" "); ok {
			return after
		}
	}
	return ""
}

// Compression works. The pages are Japanese UTF-8 (3 bytes per character) and shrink well,
// yet they used to be served uncompressed.
func TestCompressibleResponsesAreCompressed(t *testing.T) {
	router := newWebRoleTestRouter(t)

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
		t.Fatalf("cannot decompress: %v", err)
	}
	if !strings.Contains(string(body), "Bootstrap") {
		t.Error("the decompressed body is not Bootstrap's CSS")
	}
	if len(body) <= rec.Body.Len() {
		t.Errorf("compressed %d bytes is not below the original %d bytes", rec.Body.Len(), len(body))
	}
}

// The defensive headers other than CSP are on every response too. Each is a single line, yet
// the pages look fine without them, so a missing one goes unnoticed.
func TestResponsesCarrySecurityHeaders(t *testing.T) {
	router := newWebRoleTestRouter(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "same-origin",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	// autoplay is not blocked (it would break media playback).
	policy := rec.Header().Get("Permissions-Policy")
	if policy == "" {
		t.Error("Permissions-Policy is missing")
	}
	if strings.Contains(policy, "autoplay") {
		t.Errorf("Permissions-Policy blocks autoplay: %s", policy)
	}
}

// mustOIDC builds a configured verifier for tests (New returns an error when settings are missing).
func mustOIDC(t *testing.T, audience string, allowed []string) *oidc.Verifier {
	t.Helper()
	v, err := oidc.New(audience, allowed)
	if err != nil {
		t.Fatalf("oidc.New() error = %v", err)
	}
	return v
}

// TestTemplateFormActionsHaveAPostRoute checks that there is a POST route where each template
// form submits.
//
// When URLs were moved under /jobs, only the compose form was left with action="/compose".
// /compose has only GET, so submitting gave 405 and the browser showed nothing but "This page
// isn't working". The handler tests call handlers directly, so this mismatch was caught
// nowhere.
//
// An unauthenticated POST comes back as a redirect to login (or a CSRF 403), so only 404 and
// 405, which mean "no route", are looked at.
func TestTemplateFormActionsHaveAPostRoute(t *testing.T) {
	router := newWebRoleTestRouter(t)
	formAction := regexp.MustCompile(`<form[^>]*\baction="([^"]*)"`)
	templateValue := regexp.MustCompile(`\{\{[^}]*\}\}`)

	entries, err := assets.Templates.ReadDir("templates")
	if err != nil {
		t.Fatalf("ReadDir(templates) error = %v", err)
	}
	checked := 0
	for _, entry := range entries {
		raw, err := assets.Templates.ReadFile("templates/" + entry.Name())
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", entry.Name(), err)
		}
		for _, match := range formAction.FindAllStringSubmatch(string(raw), -1) {
			// Embedded values such as {{.JobID}} give the same route shape whatever the value, so put 1.
			target := templateValue.ReplaceAllString(match[1], "1")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, target, nil))
			if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
				t.Errorf("%s: form posts to %q, which has no POST route (status %d)", entry.Name(), match[1], rec.Code)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no form actions found; the pattern no longer matches the templates")
	}
}
