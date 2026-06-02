package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/sessions"

	"ap-mv/internal/ports"
	"ap-mv/internal/web/controllers"
	"ap-mv/internal/worker/event"
)

const (
	csrfSessionName = "ap-mv-csrf"
	csrfSessionKey  = "csrf_token"
)

func NewRouter(assets fs.FS, queue ports.TaskQueue, dispatcher event.Dispatcher, sessionSecret string) (http.Handler, error) {
	h, err := controllers.NewHandler(assets, queue, dispatcher)
	if err != nil {
		return nil, err
	}
	sessionSecret = strings.TrimSpace(sessionSecret)
	if sessionSecret == "" {
		return nil, fmt.Errorf("session secret is required")
	}
	csrfStore := sessions.NewCookieStore([]byte(sessionSecret))
	csrfStore.Options = &sessions.Options{
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(csrfTokenMiddleware(csrfStore))

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

func csrfTokenMiddleware(store sessions.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := store.Get(r, csrfSessionName)
			if err != nil {
				session, err = store.New(r, csrfSessionName)
				if err != nil {
					http.Error(w, "csrf session creation failed", http.StatusInternalServerError)
					return
				}
			}

			token, _ := session.Values[csrfSessionKey].(string)
			if token == "" {
				token, err = randomHex(16)
				if err != nil {
					http.Error(w, "csrf token generation failed", http.StatusInternalServerError)
					return
				}
				session.Values[csrfSessionKey] = token
				if err := session.Save(r, w); err != nil {
					http.Error(w, "csrf session save failed", http.StatusInternalServerError)
					return
				}
			}

			next.ServeHTTP(w, r.WithContext(controllers.WithCSRFToken(r.Context(), token)))
		})
	}
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
