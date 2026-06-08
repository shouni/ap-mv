package server

import (
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shouni/gcp-kit/auth"

	"ap-mv/internal/builder"
	"ap-mv/internal/server/handlers"
)

// NewRouter は、公開ルート、OAuth、認証済みWeb UI、Cloud Tasks workerルートを統合します。
func NewRouter(h *builder.AppHandlers) http.Handler {
	r := chi.NewRouter()
	setupCommonMiddleware(r)
	setupRoutes(r, h)
	return r
}

func setupCommonMiddleware(r *chi.Mux) {
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.CleanPath)
}

func setupRoutes(r chi.Router, h *builder.AppHandlers) {
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
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

		r.Use(h.Auth.Middleware)
		r.Use(csrfContextMiddleware(h.Auth))

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

func registerStaticRoutes(r chi.Router, staticFiles fs.FS) {
	r.Handle("/static/*", http.FileServer(http.FS(staticFiles)))
}

func registerWebRoutes(r chi.Router, h *handlers.Handler) {
	r.Get("/", h.Home)
	r.Route("/web", func(r chi.Router) {
		r.Get("/compose", h.ComposeForm)
		r.Post("/compose", h.PostCompose)
		r.Get("/generate-from-recipe", h.RecipeForm)
		r.Post("/generate-from-recipe", h.PostRecipe)
		r.Get("/history", h.History)
		r.Delete("/history/{jobID}", h.DeleteHistory)
	})
}

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
