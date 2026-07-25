package builder

import (
	"fmt"
	"io/fs"
	"net/url"
	"strings"

	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/app"
	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/server/handlers"
)

const defaultSessionName = "ap-mv-session"

// AppHandlers は生成されたHTTPハンドラーを保持します。
type AppHandlers struct {
	Auth        *auth.Handler
	Web         *handlers.Handler
	Worker      *worker.Handler[domain.Task]
	M2M         *auth.M2MVerifier
	StaticFiles fs.FS
}

// BuildHandlers は認証、Web、Cloud Tasks workerのハンドラーを組み立てます。
func BuildHandlers(templates fs.FS, staticFiles fs.FS, appCtx *app.Container) (*AppHandlers, error) {
	if appCtx == nil || appCtx.Config == nil {
		return nil, fmt.Errorf("app container and config are required")
	}
	if appCtx.Config.Server.ServiceURL == "" {
		return nil, fmt.Errorf("認証リダイレクトのために ServiceURL の設定が必要です")
	}
	if appCtx.Pipeline == nil {
		return nil, fmt.Errorf("pipeline is not configured")
	}

	authHandler, err := createAuthHandler(appCtx.Config)
	if err != nil {
		return nil, fmt.Errorf("認証Handlerの初期化に失敗しました: %w", err)
	}

	characterOptions, err := buildCharacterOptions()
	if err != nil {
		return nil, fmt.Errorf("キャラクター選択肢の初期化に失敗しました: %w", err)
	}
	visualOptions, err := buildVisualModeOptions()
	if err != nil {
		return nil, fmt.Errorf("visual Mode選択肢の初期化に失敗しました: %w", err)
	}

	webHandler, err := handlers.NewHandlerWithOptions(templates, appCtx.TaskQueue, handlers.ModelOptions{
		GeminiModels:       appCtx.Config.AI.GeminiModels,
		ImageModels:        appCtx.Config.AI.ImageModels,
		VeoModels:          appCtx.Config.AI.VeoModels,
		DefaultGeminiModel: appCtx.Config.AI.GeminiModel,
		DefaultImageModel:  appCtx.Config.AI.ImageModel,
		DefaultVeoModel:    appCtx.Config.AI.VeoModel,
	}, characterOptions, visualOptions)
	if err != nil {
		return nil, fmt.Errorf("WebHandlerの初期化に失敗しました: %w", err)
	}
	webHandler.HistoryRepository = appCtx.HistoryRepository
	webHandler.JobStatus = appCtx.JobStatus
	webHandler.MusicBucket = appCtx.Config.Storage.MusicBucket

	workerHandler := worker.NewHandler[domain.Task](appCtx.Pipeline)

	m2mVerifier := auth.NewM2MVerifier(appCtx.Config.Server.ServiceURL, appCtx.Config.Auth.AllowedM2MServiceAccounts)

	return &AppHandlers{
		Auth:        authHandler,
		Web:         webHandler,
		Worker:      workerHandler,
		M2M:         m2mVerifier,
		StaticFiles: staticFiles,
	}, nil
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
		if strings.HasPrefix(mode, "_") {
			continue
		}
		options.Modes = append(options.Modes, handlers.VisualModeOption{
			ID:        mode,
			Name:      visualModeDisplayName(mode),
			IsDefault: mode == assets.DefaultVisualMode,
		})
	}
	options.DefaultModeID = assets.DefaultVisualMode
	return options, nil
}

func visualModeDisplayName(mode string) string {
	switch mode {
	case "default":
		return "Default"
	case "girls_metal":
		return "Girls Metal"
	case "sparkle_rock":
		return "Sparkle Rock"
	case "techno_melancholic":
		return "Techno Melancholic"
	default:
		return mode
	}
}

// buildCharacterOptions builds character options.
func buildCharacterOptions() (handlers.CharacterOptions, error) {
	chars, err := buildCharacters()
	if err != nil {
		return handlers.CharacterOptions{}, err
	}
	options := handlers.CharacterOptions{
		Characters: make([]handlers.CharacterOption, 0, len(chars.List)),
	}
	if defaultChar := chars.GetDefault(); defaultChar != nil {
		options.DefaultCharacterID = defaultChar.ID
	}
	for _, char := range chars.List {
		options.Characters = append(options.Characters, handlers.CharacterOption{
			ID:        char.ID,
			Name:      char.Name,
			IsDefault: char.IsDefault,
			Seed:      char.Seed,
		})
	}
	return options, nil
}

// createAuthHandler は、認証ハンドラーを初期化して返します。
func createAuthHandler(cfg *config.Config) (*auth.Handler, error) {
	redirectURL, err := url.JoinPath(cfg.Server.ServiceURL, "/auth/callback")
	if err != nil {
		return nil, fmt.Errorf("リダイレクトURLの構築に失敗しました: %w", err)
	}

	return auth.NewHandler(auth.Config{
		ClientID:          cfg.Auth.GoogleClientID,
		ClientSecret:      cfg.Auth.GoogleClientSecret,
		RedirectURL:       redirectURL,
		SessionAuthKey:    cfg.Auth.SessionSecret,
		SessionEncryptKey: cfg.Auth.SessionEncryptKey,
		SessionName:       defaultSessionName,
		IsSecureCookie:    cfg.IsSecureServiceURL(),
		AllowedEmails:     cfg.Auth.AllowedEmails,
		AllowedDomains:    cfg.Auth.AllowedDomains,
		TaskAudienceURL:   cfg.Tasks.TaskAudienceURL,
		// Cloud Tasks の OIDC トークンに署名するサービスアカウント。audience は
		// 誰でも指定できる文字列に過ぎず、それだけでは呼び出し元を認証できないため、
		// 発行元サービスアカウントの照合まで行わせる（未設定だと起動時に失敗する）。
		AllowedTaskServiceAccounts: []string{cfg.GCP.ServiceAccountEmail},
	})
}
