package web

import (
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"ap-mv/internal/ports"
	"ap-mv/internal/web/controllers"
	"ap-mv/internal/worker/event"
)

func NewRouter(assets fs.FS, queue ports.TaskQueue, dispatcher event.Dispatcher) (http.Handler, error) {
	h, err := controllers.NewHandler(assets, queue, dispatcher)
	if err != nil {
		return nil, err
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(csrfTokenMiddleware)

	r.Get("/", h.Home)
	r.Route("/web", func(r chi.Router) {
		r.Get("/compose", h.ComposeForm)
		r.Post("/compose", h.PostCompose)
		r.Get("/generate-from-recipe", h.RecipeForm)
		r.Post("/generate-from-recipe", h.PostRecipe)
		r.Get("/history", h.History)
		r.Delete("/history/{jobID}", h.DeleteHistory)
	})
	r.Post("/tasks/generate", h.TaskGenerate)
	return r, nil
}

func csrfTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			http.Error(w, "csrf token generation failed", http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r.WithContext(controllers.WithCSRFToken(r.Context(), hex.EncodeToString(buf))))
	})
}
