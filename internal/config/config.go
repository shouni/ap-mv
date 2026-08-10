// Package config は、環境変数からアプリケーション設定を読み込み・正規化します。
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/shouni/ap-mv/internal/domain"
)

const taskGeneratePath = "/tasks/generate"

// ServerConfig はHTTPサーバーの起動・シャットダウンに関する設定です。
type ServerConfig struct {
	ServiceURL string `env:"SERVICE_URL" envDefault:"http://localhost:8080"`
	Port       string `env:"PORT" envDefault:"8080"`
	// Role はこのプロセスが担う役割です。明示が必須で、未設定は起動時エラーになります。
	Role            ServerRole    `env:"SERVER_ROLE"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`
}

// ServerRole はプロセスが担う役割です。Cloud Run のサービスを web と worker に
// 分けたときに、各プロセスが必要とする依存だけを構築するために使います。
type ServerRole string

const (
	// ServerRoleBoth は Web と Worker の両方を提供します（ローカル開発用）。
	ServerRoleBoth ServerRole = "both"
	// ServerRoleWeb は Web UI と M2M API だけを提供し、/tasks/generate を公開しません。
	ServerRoleWeb ServerRole = "web"
	// ServerRoleWorker は /tasks/generate だけを提供し、Web UI と OAuth を持ちません。
	ServerRoleWorker ServerRole = "worker"
)

// ParseServerRole は SERVER_ROLE の値を役割に変換します。空文字も未知の値もエラーです。
//
// 未設定を both とみなすと、本番の環境変数が 1 つ欠けただけで公開 web に
// ワーカールートが復活します。未知の値を黙って受け入れると、今度は何のルートも
// 提供しないサービスがデプロイされます。どちらも起動時に落とすほうが安全です。
func ParseServerRole(raw string) (ServerRole, error) {
	role := ServerRole(strings.ToLower(strings.TrimSpace(raw)))
	switch role {
	case ServerRoleBoth, ServerRoleWeb, ServerRoleWorker:
		return role, nil
	default:
		return "", fmt.Errorf("SERVER_ROLE (%q) は %q, %q, %q のいずれかである必要があります",
			raw, ServerRoleWeb, ServerRoleWorker, ServerRoleBoth)
	}
}

// ServesWeb は、この役割が Web 面（/web/* と OAuth）を提供するかを返します。
func (r ServerRole) ServesWeb() bool { return r == ServerRoleBoth || r == ServerRoleWeb }

// ServesWorker は、この役割が Worker 面（/tasks/generate）を提供するかを返します。
func (r ServerRole) ServesWorker() bool { return r == ServerRoleBoth || r == ServerRoleWorker }

// GCPConfig は Vertex AI / Cloud Tasks 呼び出しに使う GCP プロジェクト情報です。
type GCPConfig struct {
	ProjectID  string `env:"GCP_PROJECT_ID"`
	LocationID string `env:"GCP_LOCATION_ID"`
}

// TasksConfig は Cloud Tasks キューへのエンキュー設定です。
type TasksConfig struct {
	QueueID         string `env:"CLOUD_TASKS_QUEUE_ID"`
	WorkerURL       string `env:"WORKER_URL"`
	TaskAudienceURL string `env:"TASK_AUDIENCE_URL"`
	// CallerServiceAccountEmail は、投入するタスクの oidcToken.serviceAccountEmail に
	// 指定する caller SA です。トークンを生成して付与するのは Cloud Tasks であり、
	// このプロセスが署名するわけではありません。ap-mv は worker も継続カットを
	// 投入するため、どちらの役割でも必要です。
	CallerServiceAccountEmail string `env:"TASK_CALLER_SERVICE_ACCOUNT_EMAIL"`
	// AllowedServiceAccounts は、worker が受け付ける caller SA の許可リストです。
	// ap-mv は worker も継続カットを投入するため、web と worker の両方を並べます。
	AllowedServiceAccounts []string `env:"ALLOWED_TASK_SERVICE_ACCOUNTS"`
}

// StorageConfig は GCS バケットの設定です。
type StorageConfig struct {
	GCSBucket string `env:"AP_MV_BUCKET"`
	// MusicBucket は、Video Recipe Create の Music Job ID からレシピJSON
	// （gs://<MusicBucket>/music/<jobID>/recipe.json、ap-comp と同じ規則）を解決するために使う。
	MusicBucket string `env:"AP_MUSIC_BUCKET" envDefault:"ap-music"`
}

// AIConfig は Gemini / Image / Veo のモデルと生成パラメータです。
type AIConfig struct {
	// モデル一覧はカンマ区切りで、先頭が既定モデルです。単数形は LoadConfig が
	// 一覧の先頭から埋めるので、環境変数からは読みません。既定値は持たず、
	// 空なら ValidateEssentialConfig が起動時に落とします。
	GeminiModels []string `env:"GEMINI_MODELS"`
	ImageModels  []string `env:"IMAGE_MODELS"`
	VeoModels    []string `env:"VEO_MODELS"`
	GeminiModel  string   `env:"-"`
	ImageModel   string   `env:"-"`
	VeoModel     string   `env:"-"`

	VeoLocationID       string        `env:"VEO_LOCATION_ID"`
	VeoOutputPrefix     string        `env:"VEO_OUTPUT_PREFIX" envDefault:"github.com/shouni/ap-mv/veo"`
	VeoAspectRatio      string        `env:"VEO_ASPECT_RATIO" envDefault:"16:9"`
	VeoGenerateAudio    bool          `env:"VEO_GENERATE_AUDIO" envDefault:"false"`
	VeoPollInterval     time.Duration `env:"VEO_POLL_INTERVAL" envDefault:"10s"`
	VeoOperationTimeout time.Duration `env:"VEO_OPERATION_TIMEOUT" envDefault:"20m"`
	// PipelineTimeout はワーカータスク 1 件の実行時間の上限です。0 以下は無制限を意味します。
	// VEO_OPERATION_TIMEOUT が Veo の 1 オペレーション単位の上限であるのに対し、
	// こちらはフィルター列全体（レシピ生成・キーフレーム・動画生成・公開）を包む上限です。
	PipelineTimeout        time.Duration `env:"PIPELINE_TIMEOUT" envDefault:"45m"`
	VeoPollMaxErrors       int           `env:"VEO_POLL_MAX_ERRORS" envDefault:"10"`
	VeoUsePreviousVideo    bool          `env:"VEO_USE_PREVIOUS_VIDEO" envDefault:"false"`
	KeyframeMaxConcurrency int           `env:"KEYFRAME_MAX_CONCURRENCY" envDefault:"1"`
	KeyframeRateInterval   time.Duration `env:"KEYFRAME_RATE_INTERVAL" envDefault:"60s"`
	// VeoPriceUSDPerSec は履歴画面に出す概算コストの単価表です（モデル名=USD/生成1秒）。
	// 空キー（"=0.40" のように書く）は表に無いモデルへのフォールバックになります。
	//
	// 既定値は目安であり、実際の単価はモデル・音声生成の有無・契約で変わります。請求額と
	// 突き合わせる用途には使えません（ジョブ間の比較と無駄の検出用）。正確な数字は
	// Vertex AI の価格表を確認して、この環境変数で上書きしてください。
	VeoPriceUSDPerSec map[string]float64 `env:"VEO_PRICE_USD_PER_SEC" envDefault:"veo-3.1-generate-001:0.40,veo-3.1-fast-generate-001:0.15,veo-3.0-generate-001:0.75,veo-3.0-fast-generate-001:0.40,veo-2.0-generate-001:0.50" envSeparator:"," envKeyValSeparator:":"`
}

// NotificationConfig は外部通知先の設定です。
type NotificationConfig struct {
	SlackWebhookURL string `env:"SLACK_WEBHOOK_URL"`
}

// AuthConfig は OAuth・セッション・認可の設定です。
type AuthConfig struct {
	GoogleClientID     string `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET"`
	// SessionSecret はセッションデータのHMAC署名用シークレットキーです。
	SessionSecret string `env:"SESSION_SECRET"`
	// SessionEncryptKey はセッションデータのAES暗号化用シークレットキーです。 16, 24, 32 バイトのいずれかである必要があります。
	SessionEncryptKey string   `env:"SESSION_ENCRYPT_KEY"`
	AllowedEmails     []string `env:"ALLOWED_EMAILS"`
	AllowedDomains    []string `env:"ALLOWED_DOMAINS"`
	// AllowedM2MServiceAccounts は、Web APIをサーバー間通信（OIDC Bearerトークン）で
	// 呼び出せるサービスアカウントのメールアドレスです。空ならM2M認証は無効です。
	AllowedM2MServiceAccounts []string `env:"ALLOWED_M2M_SERVICE_ACCOUNTS"`
}

// Config はアプリ設定です。
type Config struct {
	Server       ServerConfig
	GCP          GCPConfig
	Tasks        TasksConfig
	Storage      StorageConfig
	AI           AIConfig
	Notification NotificationConfig
	Auth         AuthConfig
}

// normalize normalizes the provided values.
func (c *Config) normalize() error {
	role, err := ParseServerRole(string(c.Server.Role))
	if err != nil {
		return err
	}
	c.Server.Role = role

	workerURL, err := normalizeWorkerURL(c.Server.ServiceURL, c.Tasks.WorkerURL)
	if err != nil {
		return err
	}
	c.Tasks.WorkerURL = workerURL
	if c.Tasks.TaskAudienceURL == "" {
		c.Tasks.TaskAudienceURL = c.Server.ServiceURL
	}
	c.Storage.GCSBucket = normalizeGCSBucket(c.Storage.GCSBucket)
	c.Storage.MusicBucket = normalizeGCSBucket(c.Storage.MusicBucket)
	c.Auth.AllowedEmails = normalizeStringSlice(c.Auth.AllowedEmails)
	c.Auth.AllowedDomains = normalizeStringSlice(c.Auth.AllowedDomains)
	c.Auth.AllowedM2MServiceAccounts = normalizeStringSlice(c.Auth.AllowedM2MServiceAccounts)
	c.Tasks.AllowedServiceAccounts = normalizeStringSlice(c.Tasks.AllowedServiceAccounts)
	// Veo は提供リージョンが限られる（例: us-central1）ため、Cloud Tasks 等と共有する
	// GCP_LOCATION_ID とは別に VEO_LOCATION_ID で上書きできる。未設定なら共通値を使う。
	if strings.TrimSpace(c.AI.VeoLocationID) == "" {
		c.AI.VeoLocationID = c.GCP.LocationID
	}
	c.NormalizeModels()
	return nil
}

// NormalizeModels normalizes configured model lists and defaults.
func (c *Config) NormalizeModels() {
	// 単数形は env から読まないので、先頭に置きたいモデル（preferred）はありません。
	// ModelOptions 側は選択中のモデルを先頭へ寄せるために preferred を使います。
	c.AI.GeminiModels = domain.NormalizeModelList(c.AI.GeminiModels, "")
	c.AI.ImageModels = domain.NormalizeModelList(c.AI.ImageModels, "")
	c.AI.VeoModels = domain.NormalizeModelList(c.AI.VeoModels, "")
	c.AI.GeminiModel = domain.NormalizeDefaultModel(c.AI.GeminiModel, c.AI.GeminiModels)
	c.AI.ImageModel = domain.NormalizeDefaultModel(c.AI.ImageModel, c.AI.ImageModels)
	c.AI.VeoModel = domain.NormalizeDefaultModel(c.AI.VeoModel, c.AI.VeoModels)
}

// LoadConfigFromEnv は環境変数から設定を読み込み、変換エラーを返します。
func LoadConfigFromEnv() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, err
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// normalizeStringSlice trims strings and removes empty values.
func normalizeStringSlice(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			normalized = append(normalized, value)
		}
	}
	return normalized
}
