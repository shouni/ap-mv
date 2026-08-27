// Package server は、HTTPルーティングとミドルウェア（認証・CSRF・M2M検証）を構成します。
package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shouni/gcp-kit/cloudlog"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/builder"
	"github.com/shouni/ap-mv/internal/server/handlers"
	"github.com/shouni/gcp-kit/auth"
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
	// 画面は日本語 UTF-8（1 文字 3 バイト）なので圧縮がよく効くが、これまで無圧縮で
	// 配信していた。静的ファイルも同じ経路に乗る（vendor は immutable なので再圧縮は稀）。
	r.Use(middleware.Compress(compressionLevel))
	r.Use(securityHeaders)
}

// compressionLevel は gzip の圧縮レベルです。
const compressionLevel = 5

// contentSecurityPolicy は全レスポンスに付ける CSP です。
//
// 外部オリジンを 1 つも許可しないのは、Bootstrap を CDN から自前配信へ移したためです
// （assets/static/vendor）。CDN を allowlist に載せる形だと、jsDelivr は npm の全パッケージを
// 配信しているため「任意の npm パッケージの読み込みを許可する」に等しく、既知の
// CSP バイパス・ガジェットを持ち込まれます。'self' だけにできるのが自前配信の主目的です。
//
// インラインの <script> と on* 属性はテンプレートに 1 つも無く、
// handler_assets_test.go の TestPagesLoadTheirScripts が固定しているので、
// script-src は 'self' だけで足ります。
//
// style-src にだけ 'unsafe-inline' が要ります。テンプレートに style= 属性が残っており、
// Bootstrap の JS（collapse / tab）も遷移中にインラインスタイルを当てるためです。
//
// img-src / media-src の storage.googleapis.com は、キーフレームと動画の実体です。
// 画面が指すのは同一オリジンのパス（/web/history/{jobID}/cuts/{i}/video など）ですが、
// どれも GCS の署名付き URL へ 302 します。リダイレクト先を CSP がどう扱うかは
// ブラウザ実装に幅があるため、送り先を明示して依存しないようにしています。
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: https://storage.googleapis.com; " +
	"media-src 'self' https://storage.googleapis.com; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'none'; " +
	"form-action 'self'"

// securityHeaders は、全レスポンスに付ける防御的なヘッダー群です。
//
// hstsMaxAge は 1 年です。Cloud Run は HTTPS でしか受けないので現状の実害はありませんが、
// 独自ドメインを当てたときに平文へ降格させないための宣言です。preload は付けません
// （撤回にブラウザベンダーへの申請が要るうえ、得るものが少ないため）。
//
// Referrer-Policy を same-origin まで絞れるのは、外部オリジンへの参照を 1 つも持たないため
// です（Bootstrap を CDN から自前配信へ移した結果）。唯一の越境は署名付き URL への 302 で、
// GCS は Referer を見ません。
//
// Permissions-Policy に autoplay を入れないのは、履歴詳細がカットごとの動画を並べるためです。
// 使っていない機能だけを塞ぎます。
const hstsMaxAge = "max-age=31536000; includeSubDomains"

var securityHeaderValues = map[string]string{
	"Content-Security-Policy":   contentSecurityPolicy,
	"Strict-Transport-Security": hstsMaxAge,
	// MIME スニッフィングを止めます。
	"X-Content-Type-Options": "nosniff",
	"Referrer-Policy":        "same-origin",
	"Permissions-Policy":     "geolocation=(), camera=(), microphone=(), payment=(), usb=()",
}

// securityHeaders は、全レスポンスに securityHeaderValues を付けます。
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		for name, value := range securityHeaderValues {
			header.Set(name, value)
		}
		next.ServeHTTP(w, r)
	})
}

// setupRoutes configures routes.
func setupRoutes(r chi.Router, h *builder.AppHandlers) {
	// "/healthz" is intercepted by Cloud Run's default domain (*.run.app) at the GFE layer
	// before reaching the container (replaced with a generic 404), so avoid it.
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	setupStaticRoutes(r)

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

		r.Use(auth.Protected(h.M2M, h.Auth))

		if h.Web != nil {
			registerWebRoutes(r, h.Web)
		}
	})

	// SERVER_ROLE=web のプロセスでは TaskAuth も Worker も nil になるため、
	// このグループごと登録されず /tasks/generate は公開されません。
	// 片方だけが nil になる形は builder.AppHandlers.Validate が起動時に弾くので、
	// ここでは TaskAuth の有無だけを見れば足ります。
	r.Group(func(r chi.Router) {
		if h == nil || h.TaskAuth == nil {
			return
		}

		r.Use(auth.Require(h.TaskAuth))
		r.Post("/tasks/generate", h.Worker.ProcessTask)
	})
}

// setupStaticRoutes は、埋め込み済みの静的ファイルを /static/* で配信します。
//
// assets を直接参照します。embed.FS はバイナリに焼き込まれた定数で本番で差し替わらないため、
// 注入しても誰も通らない継ぎ目が増えるだけでした。引数で受けていた頃は、ハンドラーの
// 組み立てに失敗すると CSS まで配信されないという理由のない結合も付いていました。
func setupStaticRoutes(r chi.Router) {
	subFS, err := fs.Sub(assets.StaticFiles, "static")
	if err != nil {
		slog.Error("static assets are unavailable", "error", err)
		r.Handle("/static/*", http.NotFoundHandler())
		return
	}
	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(subFS)))
	r.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", cacheControlFor(r.URL.Path))
		fileServer.ServeHTTP(w, r)
	}))
}

// vendorPathPrefix より下は第三者製の配布物で、パスにバージョンが入っています
// （assets/static/vendor/bootstrap-5.3.8 など）。更新すれば必ず別の URL になるので、
// 再検証させる理由がありません。
const vendorPathPrefix = "/static/vendor/"

const (
	// ownAssetCacheControl は自前の CSS / JS 用です。URL を変えずに中身が変わるため短命にします。
	ownAssetCacheControl = "public, max-age=300, must-revalidate"
	// vendorCacheControl は vendorPathPrefix 配下用です。
	vendorCacheControl = "public, max-age=31536000, immutable"
)

// cacheControlFor は、静的ファイルのパスに応じた Cache-Control を返します。
//
// //go:embed した FileServer は Last-Modified も ETag も出せない（embed の ModTime が
// ゼロ値のため net/http が両方を省く）ので、期限が切れた時点で必ず全体を取り直します。
// バージョン付きの vendor を分けているのは、その再取得を無くすためです。
func cacheControlFor(path string) string {
	if strings.HasPrefix(path, vendorPathPrefix) {
		return vendorCacheControl
	}
	return ownAssetCacheControl
}

// registerWebRoutes registers web routes.
func registerWebRoutes(r chi.Router, h *handlers.Handler) {
	r.Get("/", h.Home)
	r.Get("/compose", h.VideoRecipeCreateForm)
	r.Post("/compose", h.PostVideoRecipeCreate)
	r.Get("/video-recipe-create", h.VideoRecipeCreateForm)
	r.Post("/video-recipe-create", h.PostVideoRecipeCreate)
	// 台本のみのジョブ（旧「下書き」）は compose と同じ入力で、キーフレームを焼かずに
	// カット割りまでで止まります。成果物は完成ジョブと同じ場所に保存され、履歴一覧に
	// script 段階として並びます（?stage=script で絞り込めます）。
	r.Post("/compose-draft", h.PostVideoRecipeDraft)
	r.Post("/video-recipe-draft", h.PostVideoRecipeDraft)
	// フォーム画面は履歴詳細の動画生成フォームへ統合済み。POST は ap-mcp 等の
	// M2M 呼び出しの互換性のために残している。
	r.Post("/generate-from-recipe", h.PostRecipe)
	r.Post("/mv-from-keyframe-video-recipe", h.PostRecipe)
	r.Get("/jobs/{jobID}", h.JobStatusDetail)
	r.Get("/history", h.History)
	r.Get("/history/{jobID}", h.HistoryDetail)
	r.Delete("/history/{jobID}", h.DeleteHistory)
	r.Get("/history/{jobID}/keyframes.zip", h.DownloadKeyframes)
	// 画面が指すメディアの入口。GCS の署名付き URL は HTML に出さず、ここで 302 します
	// （handler_media.go に理由）。認証グループの中にあるので、アセット 1 本ごとに
	// セッション検証が効きます。
	r.Get("/history/{jobID}/metadata", h.HistoryMetadata)
	r.Get("/history/{jobID}/video", h.HistoryVideo)
	r.Get("/history/{jobID}/cuts/{cutIndex}/video", h.CutVideo)
	r.Get("/history/{jobID}/cuts/{cutIndex}/keyframe", h.CutKeyframe)
	// レシピの読み出しと編集。表示用に整形した履歴詳細とは別経路で、読んだものを
	// そのまま直して返せます。編集は台本のみの段階に限られます（PutJobRecipe 参照）。
	r.Get("/history/{jobID}/recipe", h.GetJobRecipe)
	r.Put("/history/{jobID}/recipe", h.PutJobRecipe)
	r.Get("/history/{jobID}/cuts/{cutIndex}/regenerate", h.RegenerateCutKeyframeForm)
	r.Post("/history/{jobID}/cuts/{cutIndex}/regenerate-keyframe", h.PostRegenerateCutKeyframe)
	r.Post("/history/{jobID}/cuts/{cutIndex}/regenerate-video", h.PostRegenerateCutVideo)
	r.Get("/history/{jobID}/sections/{sectionIndex}/regenerate", h.RegenerateSectionKeyframesForm)
	r.Post("/history/{jobID}/sections/{sectionIndex}/regenerate-keyframes", h.PostRegenerateSectionKeyframes)
	// セクション単位で「キーフレーム → 動画」を進め、結果を元ジョブへ書き戻します。
	// 仕上げの結合は finalize が別途担当します（PostSectionVideo 参照）。
	r.Post("/history/{jobID}/sections/{sectionIndex}/video", h.PostSectionVideo)
	r.Post("/history/{jobID}/finalize", h.PostFinalizeVideo)
	r.Post("/history/{jobID}/regenerate-zip", h.PostRegenerateZip)
	r.Post("/history/{jobID}/generate-video", h.PostGenerateVideoFromHistory)

}
