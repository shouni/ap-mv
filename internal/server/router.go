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

// setupCommonMiddleware configures common middleware.
func setupCommonMiddleware(r *chi.Mux) {
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.CleanPath)
}

// setupRoutes configures routes.
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
		r.Get("/generate-from-recipe", h.RecipeForm)
		r.Post("/generate-from-recipe", h.PostRecipe)
		r.Get("/mv-from-keyframe-video-recipe", h.RecipeForm)
		r.Post("/mv-from-keyframe-video-recipe", h.PostRecipe)
		r.Get("/history", h.History)
		r.Delete("/history/{jobID}", h.DeleteHistory)
	})
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
