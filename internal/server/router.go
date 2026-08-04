// Package server は、HTTPルーティングとミドルウェア（認証・CSRF・M2M検証）を構成します。
package server

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/gcp-kit/cloudlog"

	"github.com/shouni/ap-mv/internal/builder"
	"github.com/shouni/ap-mv/internal/server/handlers"
)

// NewRouter は、公開ルート、OAuth、認証済みWeb UI、Cloud Tasks workerルートを統合します。
// projectID は Cloud Logging のトレース相関にのみ使用し、空なら相関を行いません。
func NewRouter(h *builder.AppHandlers, projectID string) http.Handler {
	r := chi.NewRouter()
	setupCommonMiddleware(r, projectID)
	setupRoutes(r, h)
	return r
}

// setupCommonMiddleware configures common middleware.
func setupCommonMiddleware(r *chi.Mux, projectID string) {
	// トレース相関はログ出力より先に効かせる必要があるため最初に登録する。
	r.Use(cloudlog.TraceMiddleware(projectID))
	r.Use(middleware.RequestID)
	// middleware.RealIP は X-Forwarded-For を無条件に信頼するためIPスプーフィングの脆弱性がある
	// (GHSA-3fxj-6jh8-hvhx 等）。RemoteAddr はログ出力にのみ使用しており、
	// セキュリティ判断（認証・レート制限等）には使っていないため、あえて使用しない。
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.CleanPath)
}

// setupRoutes configures routes.
func setupRoutes(r chi.Router, h *builder.AppHandlers) {
	// "/healthz" is intercepted by Cloud Run's default domain (*.run.app) at the GFE layer
	// before reaching the container (replaced with a generic 404), so avoid it.
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if h != nil && h.StaticFiles != nil {
		registerStaticRoutes(r, h.StaticFiles)
	}

	if h != nil && h.Auth != nil {
		r.Route("/auth", func(r chi.Router) {
			r.Get("/login", h.Auth.Login)
			r.Get("/callback", h.Auth.Callback)
		})
	}

	r.Group(func(r chi.Router) {
		if h == nil || h.Auth == nil {
			if h != nil && h.Web != nil {
				slog.Error("Auth handler is nil, skipping protected web routes")
			}
			return
		}

		r.Use(protectedAccessMiddleware(h.Auth, h.M2M))

		if h.Web != nil {
			registerWebRoutes(r, h.Web)
		}
	})

	r.Group(func(r chi.Router) {
		if h == nil || h.Auth == nil {
			if h != nil && h.Worker != nil {
				slog.Error("Auth handler is nil, skipping worker routes")
			}
			return
		}

		r.Use(h.Auth.TaskOIDCVerificationMiddleware)

		if h.Worker != nil {
			r.Post("/tasks/generate", h.Worker.ProcessTask)
		}
	})
}

// registerStaticRoutes registers static routes.
func registerStaticRoutes(r chi.Router, staticFiles fs.FS) {
	subFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		r.Handle("/static/*", http.NotFoundHandler())
		return
	}
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(subFS))))
}

// registerWebRoutes registers web routes.
func registerWebRoutes(r chi.Router, h *handlers.Handler) {
	r.Get("/", h.Home)
	r.Route("/web", func(r chi.Router) {
		r.Get("/compose", h.VideoRecipeCreateForm)
		r.Post("/compose", h.PostVideoRecipeCreate)
		r.Get("/video-recipe-create", h.VideoRecipeCreateForm)
		r.Post("/video-recipe-create", h.PostVideoRecipeCreate)
		// 下書き作成は compose と同じ入力で、キーフレームを焼かずにカット割りまでで止まります。
		r.Post("/compose-draft", h.PostVideoRecipeDraft)
		r.Post("/video-recipe-draft", h.PostVideoRecipeDraft)
		r.Route("/drafts", func(r chi.Router) {
			r.Get("/", h.Drafts)
			// 詳細画面は用意していません（JSON は ap-mcp 用、ブラウザは一覧へ戻ります）。
			r.Get("/{jobID}", h.Draft)
			// 直した下書きを保存し直す。読んで直して読み直すループを画像コスト0で回すため。
			r.Put("/{jobID}", h.UpdateDraft)
			r.Delete("/{jobID}", h.DeleteDraft)
		})
		// フォーム画面は履歴詳細の動画生成フォームへ統合済み。POST は ap-mcp 等の
		// M2M 呼び出しの互換性のために残している。
		r.Post("/generate-from-recipe", h.PostRecipe)
		r.Post("/mv-from-keyframe-video-recipe", h.PostRecipe)
		r.Get("/jobs/{jobID}", h.JobStatusDetail)
		r.Get("/history", h.History)
		r.Get("/history/{jobID}", h.HistoryDetail)
		r.Delete("/history/{jobID}", h.DeleteHistory)
		r.Get("/history/{jobID}/keyframes.zip", h.DownloadKeyframes)
		r.Get("/history/{jobID}/cuts/{cutIndex}/regenerate", h.RegenerateCutKeyframeForm)
		r.Post("/history/{jobID}/cuts/{cutIndex}/regenerate-keyframe", h.PostRegenerateCutKeyframe)
		r.Get("/history/{jobID}/sections/{sectionIndex}/regenerate", h.RegenerateSectionKeyframesForm)
		r.Post("/history/{jobID}/sections/{sectionIndex}/regenerate-keyframes", h.PostRegenerateSectionKeyframes)
		r.Post("/history/{jobID}/regenerate-zip", h.PostRegenerateZip)
		r.Post("/history/{jobID}/generate-video", h.PostGenerateVideoFromHistory)
	})
}

// protectedAccessMiddleware は、有効なM2M(OIDC Bearer)トークンを持つリクエストは
// セッション認証・CSRF検証をバイパスし、それ以外はブラウザセッション認証+CSRF検証にフォールバックします。
func protectedAccessMiddleware(authHandler *auth.Handler, m2m *auth.M2MVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		sessionChain := authHandler.Middleware(csrfContextMiddleware(authHandler)(next))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			payload, err := m2m.Verify(r)
			if err == nil {
				slog.Debug("M2M認証成功", "email", payload.Claims["email"], "path", r.URL.Path)
				next.ServeHTTP(w, r)
				return
			}
			// ブラウザセッションなどM2Mを試みていないリクエストはノイズになるためログしない。
			if !errors.Is(err, auth.ErrM2MNotAttempted) {
				slog.Info("M2M認証失敗、セッション認証にフォールバック", "error", err, "path", r.URL.Path)
			}
			sessionChain.ServeHTTP(w, r)
		})
	}
}

// csrfContextMiddleware returns middleware that stores CSRF tokens on the request context.
func csrfContextMiddleware(authHandler *auth.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			csrfToken := authHandler.GetCSRFTokenFromSession(r)
			if csrfToken == "" && r.Method == http.MethodGet {
				token, err := authHandler.GenerateAndSaveCSRFToken(w, r)
				if err != nil {
					slog.Error("Failed to auto-generate CSRF token", "error", err)
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					return
				}
				csrfToken = token
			}
			next.ServeHTTP(w, r.WithContext(handlers.WithCSRFToken(r.Context(), csrfToken)))
		})
	}
}
