// Package server は、HTTPルーティングとミドルウェア（認証・CSRF・M2M検証）を構成します。
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
	// 画面は日本語 UTF-8（1 文字 3 バイト）なので圧縮がよく効きます。静的ファイルも
	// 同じ経路に乗ります（vendor は immutable なので再圧縮は稀です）。
	r.Use(middleware.Compress(compressionLevel))
	r.Use(secureheaders.Middleware(secureheaders.Config{
		ImageSources: []string{gcsOrigin},
		MediaSources: []string{gcsOrigin},
		// Bootstrap の JS が遷移中にインラインスタイルを当てるため。
		AllowInlineStyle: true,
	}))
}

// compressionLevel は gzip の圧縮レベルです。
const compressionLevel = 5

// gcsOrigin は、画像と動画の実体である GCS のオリジンです。画面は同一オリジンの
// エンドポイントを指しますが、そこから署名付き URL へ 302 するため、送り先を明示します。
const gcsOrigin = "https://storage.googleapis.com"

// setupRoutes configures routes.
func setupRoutes(r chi.Router, h *builder.AppHandlers) {
	// "/healthz" is intercepted by Cloud Run's default domain (*.run.app) at the GFE layer
	// before reaching the container (replaced with a generic 404), so avoid it.
	// パスの選択理由（"/healthz" を使わない）は cloudrun.HealthPath を参照。
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

	// SERVER_ROLE=web のプロセスでは TaskAuth も Worker も nil になるため、
	// このグループごと登録されず /tasks/generate は公開されません。
	// 片方だけが nil になる形は builder.AppHandlers.Validate が起動時に弾くので、
	// ここでは TaskAuth の有無だけを見れば足ります。
	r.Group(func(r chi.Router) {
		if h == nil || h.TaskAuth == nil {
			return
		}

		r.Use(auth.Require(h.TaskAuth))
		r.Post(domain.WorkerTaskPath, h.Worker.ProcessTask)
	})
}

// setupStaticRoutes は、埋め込み済みの静的ファイルを /static/* で配信します。
// Cache-Control の判断（自前は短命、vendor は不変）とディレクトリ一覧の抑止は
// go-serve-kit の staticfiles が持ちます。
//
// 認証の外側に置きます。スタイルシートにログインを求める理由が無く、
// 未認証で表示されるログイン画面からも参照されるためです。
func setupStaticRoutes(r chi.Router) {
	files, err := staticfiles.New(staticfiles.Config{FS: assets.StaticFiles, Dir: "static"})
	if err != nil {
		// 埋め込んだ定数の取り違えなので、リクエストを受ける前に止めます。
		panic(fmt.Sprintf("static assets: %v", err))
	}
	r.Handle("/static/*", files)
}

// registerWebRoutes は Web 面のルートを登録します。
//
// ジョブが唯一の主リソースです。投入から削除まで同じ /jobs/{jobID} で指し、成果物と
// アクションはその配下に置きます（public-docs の URL 命名規約）。台本のみのジョブは
// 別の資源ではなく、同じ一覧の ?stage=script です。
func registerWebRoutes(r chi.Router, h *handlers.Handler) {
	r.Get("/", h.Home)
	// 入力フォームは JSON の対応物を持たない画面なので、資源とは別に置きます。
	r.Get("/compose", h.ComposeForm)

	r.Route("/jobs", func(r chi.Router) {
		r.Post("/", h.JobCreate)
		r.Get("/", h.JobList)
		r.Get("/{jobID}", h.Job)
		r.Delete("/{jobID}", h.JobDelete)
		// 成果物。GCS の署名付き URL は HTML に出さず、ここで 302 します（job_media.go に理由）。
		// 認証グループの中にあるので、アセット 1 本ごとにセッション検証が効きます。
		r.Get("/{jobID}/keyframes", h.JobKeyframes)
		r.Get("/{jobID}/metadata", h.JobMetadata)
		r.Get("/{jobID}/video", h.JobVideo)
		r.Get("/{jobID}/cuts/{cutIndex}/video", h.CutVideo)
		r.Get("/{jobID}/cuts/{cutIndex}/keyframe", h.CutKeyframe)
		// レシピの読み出しと編集。表示用に整形した詳細とは別経路で、読んだものを
		// そのまま直して返せます。編集は台本のみの段階に限られます（JobRecipeUpdate 参照）。
		r.Get("/{jobID}/recipe", h.JobRecipe)
		r.Put("/{jobID}/recipe", h.JobRecipeUpdate)
		// アクション。作り直し系は成果物を元ジョブへ書き戻し、動画生成は新しいジョブになります。
		r.Get("/{jobID}/cuts/{cutIndex}/regenerate", h.RegenerateCutKeyframeForm)
		r.Post("/{jobID}/cuts/{cutIndex}/regenerate-keyframe", h.RegenerateCutKeyframe)
		r.Post("/{jobID}/cuts/{cutIndex}/regenerate-video", h.RegenerateCutVideo)
		r.Get("/{jobID}/sections/{sectionIndex}/regenerate", h.RegenerateSectionKeyframesForm)
		r.Post("/{jobID}/sections/{sectionIndex}/regenerate-keyframes", h.RegenerateSectionKeyframes)
		// セクション単位で「キーフレーム → 動画」を進め、結果を元ジョブへ書き戻します。
		// 仕上げの結合は finalize が別途担当します（GenerateSectionVideo 参照）。
		r.Post("/{jobID}/sections/{sectionIndex}/generate-video", h.GenerateSectionVideo)
		r.Post("/{jobID}/finalize", h.Finalize)
		r.Post("/{jobID}/regenerate-zip", h.RegenerateZip)
		r.Post("/{jobID}/generate-video", h.GenerateVideo)
	})
}
