package builder

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"

	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/gcp-kit/worker"
	promptkit "github.com/shouni/go-prompt-kit/prompts"

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
	// TaskAuth は Cloud Tasks からの OIDC を検証します。Auth と違い OAuth 設定を
	// 必要としないため、Web 面を持たない Worker プロセスでも構築できます。
	TaskAuth *auth.TaskVerifier
}

// Validate は、組み立て結果が役割として筋の通った形になっていることを確かめます。
//
// TaskAuth と Worker は「Cloud Tasks の検証」と「その先の処理」で対になっており、
// 片方だけが nil なのは DI の不整合です。router.go は nil を見てルート登録を省くため、
// 放置すると /tasks/generate が黙って 404 になるだけで、原因が設定なのか実装なのか
// リクエストからは区別できません。ルーターが 404 を返す前に起動を失敗させます。
func (h *AppHandlers) Validate() error {
	if (h.TaskAuth == nil) != (h.Worker == nil) {
		return errors.New("TaskAuth と Worker は同時に構成する必要があります")
	}
	return nil
}

// BuildHandlers は各ハンドラーの依存関係を SERVER_ROLE に応じて組み立てます。
// 担当しない面のハンドラーは nil のままにし、router 側でルート登録ごと省かれるようにします。
func BuildHandlers(templates fs.FS, staticFiles fs.FS, appCtx *app.Container) (*AppHandlers, error) {
	if appCtx == nil || appCtx.Config == nil {
		return nil, fmt.Errorf("app container and config are required")
	}
	if appCtx.Config.Server.ServiceURL == "" {
		return nil, fmt.Errorf("認証リダイレクトのために ServiceURL の設定が必要です")
	}

	h := &AppHandlers{StaticFiles: staticFiles}
	role := appCtx.Config.Server.Role

	if role.ServesWeb() {
		if err := buildWebHandlers(templates, appCtx, h); err != nil {
			return nil, err
		}
	}

	if role.ServesWorker() {
		if appCtx.Pipeline == nil {
			return nil, fmt.Errorf("pipeline is not configured")
		}
		// audience と許可する caller SA の両方が揃わないと検証は常に失敗する
		// （fail-closed）ため、起動時に構成を確かめておきます。
		taskAuth := auth.NewTaskVerifier(
			appCtx.Config.Tasks.TaskAudienceURL,
			appCtx.Config.Tasks.AllowedServiceAccounts,
		)
		if !taskAuth.Configured() {
			return nil, fmt.Errorf("cloud Tasks の OIDC 検証を構成できません: TASK_AUDIENCE_URL と ALLOWED_TASK_SERVICE_ACCOUNTS が必要です")
		}
		h.TaskAuth = taskAuth
		h.Worker = worker.NewHandler[domain.Task](appCtx.Pipeline)
	}

	if err := h.Validate(); err != nil {
		return nil, err
	}

	return h, nil
}

// buildWebHandlers は Web 面（OAuth・Web UI・M2M 検証）のハンドラーを組み立てます。
func buildWebHandlers(templates fs.FS, appCtx *app.Container, h *AppHandlers) error {
	authHandler, err := createAuthHandler(appCtx.Config)
	if err != nil {
		return fmt.Errorf("認証Handlerの初期化に失敗しました: %w", err)
	}

	characterOptions, err := buildCharacterOptions()
	if err != nil {
		return fmt.Errorf("キャラクター選択肢の初期化に失敗しました: %w", err)
	}
	visualOptions, err := buildVisualModeOptions()
	if err != nil {
		return fmt.Errorf("visual Mode選択肢の初期化に失敗しました: %w", err)
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
		return fmt.Errorf("WebHandlerの初期化に失敗しました: %w", err)
	}
	webHandler.HistoryRepository = appCtx.HistoryRepository
	webHandler.JobStatus = appCtx.JobStatus
	webHandler.MusicBucket = appCtx.Config.Storage.MusicBucket
	webHandler.VeoPricing = domain.VeoPricing(appCtx.Config.AI.VeoPriceUSDPerSec)

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
		// 部品は他テンプレートからの参照専用で、選択肢に出す名前ではありません。
		// 判定は Builder が Build の対象を決めるのと同じ関数に任せます。
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
	})
}

// newM2MVerifier は M2M(サーバー間通信)用の OIDC 検証器を構成します。
//
// ProtectedMiddleware は M2M を無効化できません。許可リストか audience が欠けていても
// 経路は生き続け、検証が必ず失敗してセッション認証へフォールバックします。つまり設定漏れは
// 「ブラウザは正常に動くが ap-mcp だけログイン画面の HTML を受け取る」という形でしか
// 現れません。意図的な無効化と設定漏れを区別する手段が無い以上、空は後者としか解釈できない
// ので、TaskVerifier と同じく起動時に弾きます。
//
// 構成の可否を config ではなく検証器自身に尋ねるのは、必要な設定が何かを知っているのが
// gcp-kit 側だからです。許可リストの空だけを config で見ると audience（SERVICE_URL）の
// 欠落を拾えず、kit が要件を増やしても追随しません。
func newM2MVerifier(serviceURL string, allowedServiceAccounts []string) (*auth.M2MVerifier, error) {
	m2m := auth.NewM2MVerifier(serviceURL, allowedServiceAccounts)
	if !m2m.Configured() {
		return nil, fmt.Errorf("m2m の OIDC 検証を構成できません: SERVICE_URL と ALLOWED_M2M_SERVICE_ACCOUNTS が必要です")
	}
	return m2m, nil
}
