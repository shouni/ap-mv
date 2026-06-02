package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"ap-mv/internal/ports"
	"ap-mv/internal/worker/event"
)

var csrfInputPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func TestComposePostRequiresSessionCSRFToken(t *testing.T) {
	router, err := NewRouter(os.DirFS("../.."), ports.InlineTaskQueue{}, event.Dispatcher{}, "test-session-secret")
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/web/compose", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /web/compose status = %d, want %d", getRec.Code, http.StatusOK)
	}
	matches := csrfInputPattern.FindStringSubmatch(getRec.Body.String())
	if len(matches) != 2 {
		t.Fatalf("GET /web/compose did not render csrf token")
	}
	cookies := getRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("GET /web/compose did not set session cookie")
	}

	forbiddenReq := newComposePostRequest("")
	for _, cookie := range cookies {
		forbiddenReq.AddCookie(cookie)
	}
	forbiddenRec := httptest.NewRecorder()
	router.ServeHTTP(forbiddenRec, forbiddenReq)
	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("POST /web/compose without csrf status = %d, want %d", forbiddenRec.Code, http.StatusForbidden)
	}

	acceptedReq := newComposePostRequest(matches[1])
	for _, cookie := range cookies {
		acceptedReq.AddCookie(cookie)
	}
	acceptedRec := httptest.NewRecorder()
	router.ServeHTTP(acceptedRec, acceptedReq)
	if acceptedRec.Code != http.StatusAccepted {
		t.Fatalf("POST /web/compose with csrf status = %d, want %d; body=%s", acceptedRec.Code, http.StatusAccepted, acceptedRec.Body.String())
	}
}

func newComposePostRequest(csrfToken string) *http.Request {
	form := url.Values{
		"text": {"test compose input"},
	}
	if csrfToken != "" {
		form.Set("csrf_token", csrfToken)
	}
	req := httptest.NewRequest(http.MethodPost, "/web/compose", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}
