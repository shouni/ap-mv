package builder

import (
	"errors"
	"fmt"

	"github.com/shouni/gcp-kit/auth/oidc"
	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/gcp-kit/worker"
	promptkit "github.com/shouni/go-prompt-kit/prompts"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/app"
	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/server/handlers"
)

const defaultSessionName = "ap-mv-session"

// AppHandlers holds the HTTP handlers built.
type AppHandlers struct {
	Auth   *session.Handler
	Web    *handlers.Handler
	Worker *worker.Handler[domain.Task]
	M2M    *oidc.Verifier
	// TaskAuth verifies OIDC from Cloud Tasks. Unlike Auth it needs no OAuth settings, so it can be
	// built in a worker process without a web face.
	TaskAuth *oidc.Verifier
}

// Validate checks that the assembled result has a shape that makes sense for the role.
//
// TaskAuth and Worker pair up as "verifying Cloud Tasks" and "the processing behind it", and only
// one being nil is a DI inconsistency. router.go omits route registration on nil, so left alone,
// /tasks/generate would just silently 404 and a request could not tell whether the cause is
// configuration or implementation. Startup fails before the router ever returns 404.
func (h *AppHandlers) Validate() error {
	if (h.TaskAuth == nil) != (h.Worker == nil) {
		return errors.New("TaskAuth and Worker must be configured together")
	}
	return nil
}

// BuildHandlers assembles each handler's dependencies according to SERVER_ROLE.
// Handlers of faces it does not serve stay nil, so the router omits their route registration.
func BuildHandlers(appCtx *app.Container) (*AppHandlers, error) {
	if appCtx == nil || appCtx.Config == nil {
		return nil, fmt.Errorf("app container and config are required")
	}
	if appCtx.Config.Server.ServiceURL == "" {
		return nil, fmt.Errorf("ServiceURL must be set for authentication redirects")
	}

	h := &AppHandlers{}
	role := appCtx.Config.Server.Role

	if role.ServesWeb() {
		if err := buildWebHandlers(appCtx, h); err != nil {
			return nil, err
		}
	}

	if role.ServesWorker() {
		if appCtx.Pipeline == nil {
			return nil, fmt.Errorf("pipeline is not configured")
		}
		// Without both an audience and allowed caller SAs, verification always fails (fail-closed), so
		// check the configuration at startup.
		taskAuth, err := oidc.New(appCtx.Config.Tasks.TaskAudienceURL, appCtx.Config.Tasks.AllowedServiceAccounts)
		if err != nil {
			return nil, fmt.Errorf("cannot configure Cloud Tasks OIDC verification: TASK_AUDIENCE_URL and ALLOWED_TASK_SERVICE_ACCOUNTS are required: %w", err)
		}
		h.TaskAuth = taskAuth
		h.Worker = worker.NewHandler[domain.Task](appCtx.Pipeline)
	}

	if err := h.Validate(); err != nil {
		return nil, err
	}

	return h, nil
}

// buildWebHandlers assembles the web face's handlers (OAuth, web UI, M2M verification).
func buildWebHandlers(appCtx *app.Container, h *AppHandlers) error {
	authHandler, err := createAuthHandler(appCtx.Config, appCtx.SessionStore)
	if err != nil {
		return fmt.Errorf("failed to initialise the auth handler: %w", err)
	}

	characterOptions, err := buildCharacterOptions()
	if err != nil {
		return fmt.Errorf("failed to initialise the character options: %w", err)
	}
	visualOptions, err := buildVisualModeOptions()
	if err != nil {
		return fmt.Errorf("failed to initialise the visual mode options: %w", err)
	}

	webHandler, err := handlers.NewHandlerWithOptions(assets.Templates, appCtx.TaskQueue, handlers.ModelOptions{
		GeminiModels:       appCtx.Config.AI.GeminiModels,
		ImageModels:        appCtx.Config.AI.ImageModels,
		VeoModels:          appCtx.Config.AI.VeoModels,
		DefaultGeminiModel: appCtx.Config.AI.GeminiModel,
		DefaultImageModel:  appCtx.Config.AI.ImageModel,
		DefaultVeoModel:    appCtx.Config.AI.VeoModel,
	}, characterOptions, visualOptions)
	if err != nil {
		return fmt.Errorf("failed to initialise the web handler: %w", err)
	}
	webHandler.HistoryRepository = appCtx.HistoryRepository
	webHandler.JobStatus = appCtx.JobStatus
	webHandler.MusicBucket = appCtx.Config.Storage.MusicBucket
	webHandler.VeoPricing = domain.VeoPricing(appCtx.Config.AI.VeoPriceUSDPerSec)
	if webHandler.Characters, err = buildCharacters(); err != nil {
		return fmt.Errorf("failed to initialise the characters: %w", err)
	}

	h.Auth = authHandler
	h.Web = webHandler
	m2m, err := newM2MVerifier(appCtx.Config.Server.ServiceURL, appCtx.Config.Auth.AllowedM2MServiceAccounts)
	if err != nil {
		return err
	}
	h.M2M = m2m

	return nil
}

func buildVisualModeOptions() (handlers.VisualModeOptions, error) {
	templates, err := assets.LoadVisualModeFiles()
	if err != nil {
		return handlers.VisualModeOptions{}, err
	}
	options := handlers.VisualModeOptions{
		Modes: make([]handlers.VisualModeOption, 0, len(templates)),
	}
	for mode := range templates {
		// Partials are only referenced from other templates, not names to offer as options.
		// The decision is left to the same function Builder uses to decide what to Build.
		if promptkit.IsPartial(mode, promptkit.DefaultPartialPrefix) {
			continue
		}
		options.Modes = append(options.Modes, handlers.VisualModeOption{
			ID:        mode,
			Name:      handlers.DisplayVisualModeName(mode),
			IsDefault: mode == assets.DefaultVisualMode,
		})
	}
	options.DefaultModeID = assets.DefaultVisualMode
	return options, nil
}

// buildCharacterOptions builds character options.
func buildCharacterOptions() (handlers.CharacterOptions, error) {
	chars, err := buildCharacters()
	if err != nil {
		return handlers.CharacterOptions{}, err
	}
	options := handlers.CharacterOptions{
		Characters: make([]handlers.CharacterOption, 0, chars.Len()),
	}
	if defaultChar := chars.GetDefault(); defaultChar != nil {
		options.DefaultCharacterID = defaultChar.ID
	}
	for _, char := range chars.All() {
		options.Characters = append(options.Characters, handlers.CharacterOption{
			ID:        char.ID,
			Name:      char.Name,
			IsDefault: char.IsDefault,
			Seed:      char.Seed,
		})
	}
	return options, nil
}

// createAuthHandler initialises and returns the authentication handler.
func createAuthHandler(cfg *config.Config, store session.Store) (*session.Handler, error) {
	return session.New(session.Config{
		ClientID:       cfg.Auth.GoogleClientID,
		ClientSecret:   cfg.Auth.GoogleClientSecret,
		ServiceURL:     cfg.Server.ServiceURL,
		Store:          store,
		SessionName:    defaultSessionName,
		AllowedEmails:  cfg.Auth.AllowedEmails,
		AllowedDomains: cfg.Auth.AllowedDomains,
	})
}

// newM2MVerifier configures the OIDC verifier for M2M (server-to-server communication).
//
// auth.Protected cannot disable M2M. Even with the allowlist or audience missing the path stays
// alive, verification always fails and falls back to session authentication. So a missing setting
// shows up only as "the browser works fine but the MCP gateway alone receives the login page's
// HTML". With no way to tell a deliberate disable from a missing setting, empty can only mean the
// latter, so like TaskVerifier it is rejected at startup.
//
// The verifier itself, rather than config, is asked whether it can be configured, because gcp-kit
// is what knows which settings are required. Checking only an empty allowlist in config would miss
// a missing audience (SERVICE_URL) and would not follow if the kit added requirements.
func newM2MVerifier(serviceURL string, allowedServiceAccounts []string) (*oidc.Verifier, error) {
	m2m, err := oidc.New(serviceURL, allowedServiceAccounts)
	if err != nil {
		return nil, fmt.Errorf("cannot configure M2M OIDC verification (SERVICE_URL and ALLOWED_M2M_SERVICE_ACCOUNTS): %w", err)
	}
	return m2m, nil
}
