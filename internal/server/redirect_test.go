package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 旧 /web/... のリンクとブックマークが生き続けること。
//
// 308 なのは POST のメソッドと本文を保つためです。302 だと GET に落ち、
// ap-mcp からの投入が黙って失われます。
func TestRedirectFromWebPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{"一覧", http.MethodGet, "/web/history", "/history"},
		{"クエリを保つ", http.MethodGet, "/web/history?stage=script", "/history?stage=script"},
		{"POST も転送する", http.MethodPost, "/web/history/job-1/finalize", "/history/job-1/finalize"},
		{"プレフィックスだけなら root へ", http.MethodGet, "/web", "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			http.HandlerFunc(redirectFromWebPrefix).ServeHTTP(rec, req)

			if rec.Code != http.StatusPermanentRedirect {
				t.Fatalf("status = %d, want %d（メソッドを保つ転送）", rec.Code, http.StatusPermanentRedirect)
			}
			if got := rec.Header().Get("Location"); got != tt.want {
				t.Errorf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}
