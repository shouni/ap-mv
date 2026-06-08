package builder

import (
	"fmt"
	"io/fs"
	"net/url"

	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/gcp-kit/worker"

	"ap-mv/internal/app"
	"ap-mv/internal/config"
	"ap-mv/internal/domain"
	"ap-mv/internal/server/handlers"
)

const defaultSessionName = "ap-mv-session"

// AppHandlers は生成されたHTTPハンドラーを保持します。
type AppHandlers struct {
	Auth        *auth.Handler
	Web         *handlers.Handler
	Worker      *worker.Handler[domain.Task]
	StaticFiles fs.FS
}

// BuildHandlers は認証、Web、Cloud Tasks workerのハンドラーを組み立てます。
func BuildHandlers(templates fs.FS, staticFiles fs.FS, appCtx *app.Container) (*AppHandlers, error) {
	if appCtx == nil || appCtx.Config == nil {
		return nil, fmt.Errorf("app container and config are required")
	}
	if appCtx.Config.ServiceURL == "" {
		return nil, fmt.Errorf("認証リダイレクトのために ServiceURL の設定が必要です")
	}
	if appCtx.Pipeline == nil {
		return nil, fmt.Errorf("pipeline is not configured")
	}

	authHandler, err := createAuthHandler(appCtx.Config)
	if err != nil {
		return nil, fmt.Errorf("認証Handlerの初期化に失敗しました: %w", err)
	}

	webHandler, err := handlers.NewHandler(templates, appCtx.TaskQueue, handlers.ModelOptions{
		GeminiModels:       appCtx.Config.GeminiModels,
		ImageModels:        appCtx.Config.ImageModels,
		DefaultGeminiModel: appCtx.Config.GeminiModel,
		DefaultImageModel:  appCtx.Config.ImageModel,
	})
	if err != nil {
		return nil, fmt.Errorf("WebHandlerの初期化に失敗しました: %w", err)
	}

	workerHandler := worker.NewHandler[domain.Task](appCtx.Pipeline)

	return &AppHandlers{
		Auth:        authHandler,
		Web:         webHandler,
		Worker:      workerHandler,
		StaticFiles: staticFiles,
	}, nil
}

// createAuthHandler は、認証ハンドラーを初期化して返します。
func createAuthHandler(cfg *config.Config) (*auth.Handler, error) {
	redirectURL, err := url.JoinPath(cfg.ServiceURL, "/auth/callback")
	if err != nil {
		return nil, fmt.Errorf("リダイレクトURLの構築に失敗しました: %w", err)
	}

	return auth.NewHandler(auth.Config{
		ClientID:          cfg.GoogleClientID,
		ClientSecret:      cfg.GoogleClientSecret,
		RedirectURL:       redirectURL,
		SessionAuthKey:    cfg.SessionSecret,
		SessionEncryptKey: cfg.SessionEncryptKey,
		SessionName:       defaultSessionName,
		IsSecureCookie:    cfg.IsSecureServiceURL(),
		AllowedEmails:     cfg.AllowedEmails,
		AllowedDomains:    cfg.AllowedDomains,
		TaskAudienceURL:   cfg.TaskAudienceURL,
	})
}
