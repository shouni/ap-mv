// Package server configures HTTP routing and middleware (authentication, CSRF, M2M verification).
package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/gcp-kit/cloudlog"
	"github.com/shouni/gcp-kit/cloudrun"
	"github.com/shouni/go-serve-kit/secureheaders"
	"github.com/shouni/go-serve-kit/staticfiles"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/builder"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/server/handlers"
)

// NewRouter combines the public routes, OAuth, the authenticated web UI and the Cloud Tasks worker route.
// projectID is used only for Cloud Logging trace correlation; empty disables correlation.
func NewRouter(h *builder.AppHandlers, projectID string) http.Handler {
	r := chi.NewRouter()
	setupCommonMiddleware(r, projectID)
	setupRoutes(r, h)
	return r
}

// setupCommonMiddleware configures common middleware.
func setupCommonMiddleware(r *chi.Mux, projectID string) {
	// Trace correlation has to take effect before logging, so it is registered first.
	r.Use(cloudlog.TraceMiddleware(projectID))
	r.Use(middleware.RequestID)
	// middleware.RealIP trusts X-Forwarded-For unconditionally, which is an IP spoofing vulnerability
	// (GHSA-3fxj-6jh8-hvhx and others). RemoteAddr is used only for logging, not for any
	// security decision (authentication, rate limiting, etc.), so it is deliberately not used.
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.CleanPath)
	// The pages are Japanese UTF-8 (3 bytes per character), so compression works well. Static
	// files go through the same path (vendor is immutable, so recompression is rare).
	r.Use(middleware.Compress(compressionLevel))
	r.Use(secureheaders.Middleware(secureheaders.Config{
		ImageSources: []string{gcsOrigin},
		MediaSources: []string{gcsOrigin},
		// Because Bootstrap's JS applies inline styles during transitions.
		AllowInlineStyle: true,
	}))
}

// compressionLevel is the gzip compression level.
const compressionLevel = 5

// gcsOrigin is the GCS origin that holds the actual images and videos. The pages point at
// same-origin endpoints, but those 302 to signed URLs, so the destination is named explicitly.
const gcsOrigin = "https://storage.googleapis.com"

// setupRoutes configures routes.
func setupRoutes(r chi.Router, h *builder.AppHandlers) {
	// "/healthz" is intercepted by Cloud Run's default domain (*.run.app) at the GFE layer
	// before reaching the container (replaced with a generic 404), so avoid it.
	// For why this path (not "/healthz") was chosen, see cloudrun.HealthPath.
	r.Get(cloudrun.HealthPath, cloudrun.Health)
	setupStaticRoutes(r)

	if h != nil && h.Auth != nil {
		r.Handle("/auth/*", h.Auth.Routes()) // login / callback / logout
	}

	r.Group(func(r chi.Router) {
		if h == nil || h.Auth == nil {
			if h != nil && h.Web != nil {
				slog.Error("Auth handler is nil, skipping protected web routes")
			}
			return
		}

		r.Use(auth.Protected(h.M2M, h.Auth))

		if h.Web != nil {
			registerWebRoutes(r, h.Web)
		}
	})

	// In a SERVER_ROLE=web process TaskAuth and Worker are both nil, so this whole group is not
	// registered and /tasks/generate is not exposed.
	// A shape where only one of them is nil is rejected at startup by builder.AppHandlers.Validate,
	// so checking TaskAuth alone is enough here.
	r.Group(func(r chi.Router) {
		if h == nil || h.TaskAuth == nil {
			return
		}

		r.Use(auth.Require(h.TaskAuth))
		r.Post(domain.WorkerTaskPath, h.Worker.ProcessTask)
	})
}

// setupStaticRoutes serves the embedded static files at /static/*.
// The Cache-Control decision (short-lived for our own, immutable for vendor) and suppressing
// directory listings belong to go-serve-kit's staticfiles.
//
// It sits outside authentication. There is no reason to demand a login for a stylesheet, and
// the login page, shown unauthenticated, refers to them too.
func setupStaticRoutes(r chi.Router) {
	files, err := staticfiles.New(staticfiles.Config{FS: assets.StaticFiles, Dir: "static"})
	if err != nil {
		// This would be a mix-up of embedded constants, so stop before accepting any request.
		panic(fmt.Sprintf("static assets: %v", err))
	}
	r.Handle("/static/*", files)
}

// registerWebRoutes registers the web face's routes.
//
// The job is the only primary resource. It is named by the same /jobs/{jobID} from submission
// to deletion, with artifacts and actions under it (public-docs' URL naming conventions).
// Script-only jobs are not a separate resource but ?stage=script on the same listing.
func registerWebRoutes(r chi.Router, h *handlers.Handler) {
	r.Get("/", h.Home)
	// Input forms are pages with no JSON counterpart, so they sit apart from the resources.
	r.Get("/compose", h.ComposeForm)

	r.Route("/jobs", func(r chi.Router) {
		r.Post("/", h.JobCreate)
		r.Get("/", h.JobList)
		r.Get("/{jobID}", h.Job)
		r.Delete("/{jobID}", h.JobDelete)
		// Artifacts. GCS signed URLs are not put in HTML; these 302 instead (reasons in job_media.go).
		// They are inside the authenticated group, so the session is checked for every asset.
		r.Get("/{jobID}/keyframes", h.JobKeyframes)
		r.Get("/{jobID}/metadata", h.JobMetadata)
		r.Get("/{jobID}/video", h.JobVideo)
		r.Get("/{jobID}/cuts/{cutIndex}/video", h.CutVideo)
		r.Get("/{jobID}/cuts/{cutIndex}/keyframe", h.CutKeyframe)
		// Reading and editing the recipe. A separate path from the detail formatted for display, so
		// what is read can be edited and sent straight back. Editing is limited to the script-only stage (see JobRecipeUpdate).
		r.Get("/{jobID}/recipe", h.JobRecipe)
		r.Put("/{jobID}/recipe", h.JobRecipeUpdate)
		r.Get("/{jobID}/prompt", h.JobPrompt)
		// Actions. Remakes write their artifacts back to the original job; video generation makes a new job.
		r.Get("/{jobID}/cuts/{cutIndex}/regenerate", h.RegenerateCutKeyframeForm)
		r.Post("/{jobID}/cuts/{cutIndex}/regenerate-keyframe", h.RegenerateCutKeyframe)
		r.Post("/{jobID}/cuts/{cutIndex}/regenerate-video", h.RegenerateCutVideo)
		r.Get("/{jobID}/sections/{sectionIndex}/regenerate", h.RegenerateSectionKeyframesForm)
		r.Post("/{jobID}/sections/{sectionIndex}/regenerate-keyframes", h.RegenerateSectionKeyframes)
		// Advances "keyframes → video" per section and writes the result back to the original job.
		// The final join is done separately by finalize (see GenerateSectionVideo).
		r.Post("/{jobID}/sections/{sectionIndex}/generate-video", h.GenerateSectionVideo)
		r.Post("/{jobID}/finalize", h.Finalize)
		r.Post("/{jobID}/regenerate-zip", h.RegenerateZip)
		r.Post("/{jobID}/generate-video", h.GenerateVideo)
	})
}
