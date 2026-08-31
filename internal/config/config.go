// Package config は、環境変数からアプリケーション設定を読み込み・正規化します。
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-serve-kit/serverrole"
	"github.com/shouni/go-utils/strlist"

	"github.com/caarlos0/env/v11"

	"github.com/shouni/ap-mv/internal/domain"
)

const taskGeneratePath = "/tasks/generate"

// ServerConfig はHTTPサーバーの起動・シャットダウンに関する設定です。
type ServerConfig struct {
	ServiceURL string `env:"SERVICE_URL" envDefault:"http://localhost:8080"`
	Port       string `env:"PORT" envDefault:"8080"`
	// Role はこのプロセスが担う役割です。明示が必須で、未設定は起動時エラーになります。
	Role            serverrole.Role `env:"SERVER_ROLE"`
	ShutdownTimeout time.Duration   `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`
}

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
	// DispatchDeadline は、投入するタスクに載せる応答待ちの上限です。
	//
	// 「待つ時間」ではなく **ワーカーの実行時間の実効上限** です。これを超えると
	// ワーカーがまだ処理中でも Cloud Tasks が待受を打ち切り、キューは max_attempts = 1 なので
	// 再試行も来ません。Cloud Run の timeout をいくら伸ばしてもこの上限は動きません。
	// 定数ではなく env なのは、この値をインフラ側（Terraform）が唯一の出どころとして
	// 持てるようにするためです。定数だとインフラが写しを抱え、ズレても誰も気付きません。
	//
	// **既定値は持ちません。** 三段のタイムアウトはデプロイ先の事情で決まる値なので、
	// 出どころは Terraform 1 箇所に閉じます。アプリが既定を持つと同じ数字が 2 箇所に
	// 現れ、設定漏れが「誰も選んでいない値」で動いてしまいます。
	DispatchDeadline time.Duration `env:"TASK_DISPATCH_DEADLINE"`
	// PipelineTimeout はワーカータスク 1 件の実行時間の上限です。DispatchDeadline と
	// 対で三段のタイムアウトを成すため、AI の生成パラメータではなくここに置きます
	// （VEO_OPERATION_TIMEOUT が Veo の 1 オペレーション単位の上限であるのに対し、
	// こちらはフィルター列全体（レシピ生成・キーフレーム・動画生成・公開）を包みます）。
	//
	// DispatchDeadline より短く取ります。等号でも駄目で、アプリが先に諦められないと
	// 失敗の記録も Slack 通知も出ないまま Cloud Tasks に打ち切られます
	// （worker では validatePipelineTimeout が起動時に拒否します）。既定値は
	// DispatchDeadline と同じ理由で持ちません。
	PipelineTimeout time.Duration `env:"PIPELINE_TIMEOUT"`
}

// StorageConfig は GCS バケットの設定です。
type StorageConfig struct {
	GCSBucket   string `env:"AP_MV_BUCKET"`
	MusicBucket string `env:"AP_MUSIC_BUCKET"`
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

	VeoLocationID string `env:"VEO_LOCATION_ID"`
	// VeoOutputPrefix は既定値を持ちません。生成物の置き場所はデプロイ先が決める値で、
	// アプリが既定を持つと設定漏れが「誰も選んでいない場所」に書き込んで動いてしまいます。
	// 空なら ValidateEssentialConfig が起動時に落とします。
	VeoOutputPrefix        string        `env:"VEO_OUTPUT_PREFIX"`
	VeoAspectRatio         string        `env:"VEO_ASPECT_RATIO" envDefault:"16:9"`
	VeoGenerateAudio       bool          `env:"VEO_GENERATE_AUDIO" envDefault:"false"`
	VeoPollInterval        time.Duration `env:"VEO_POLL_INTERVAL" envDefault:"10s"`
	VeoOperationTimeout    time.Duration `env:"VEO_OPERATION_TIMEOUT" envDefault:"20m"`
	VeoPollMaxErrors       int           `env:"VEO_POLL_MAX_ERRORS" envDefault:"10"`
	VeoUsePreviousVideo    bool          `env:"VEO_USE_PREVIOUS_VIDEO" envDefault:"false"`
	KeyframeMaxConcurrency int           `env:"KEYFRAME_MAX_CONCURRENCY" envDefault:"1"`
	KeyframeRateInterval   time.Duration `env:"KEYFRAME_RATE_INTERVAL" envDefault:"60s"`
	// KeyframeImageSize はキーフレーム画像の出力解像度です（"1K" / "2K" / "4K"）。
	// go-veo-orchestrator は画作りの既定値を持たないため、ここが唯一の出所です。
	KeyframeImageSize string `env:"KEYFRAME_IMAGE_SIZE" envDefault:"2K"`
	// VeoPriceUSDPerSec は履歴画面に出す概算コストの単価表です（モデル名:USD/生成1秒）。
	// 空キー（":0.40" のように書く）は表に無いモデルへのフォールバックになります。
	//
	// 既定値は持ちません。モデル名と価格はどちらも Google 側の都合で変わるため、
	// 表そのものをデプロイ設定（ap-infra）に置きます。未設定なら全モデルが
	// domain.DefaultVeoPriceUSDPerSecond になり、表示は壊れません。
	VeoPriceUSDPerSec map[string]float64 `env:"VEO_PRICE_USD_PER_SEC" envSeparator:"," envKeyValSeparator:":"`
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
	c.trimStringValues()
	// 環境変数名はアプリ側の関心事なので、キットのエラーへここで文脈を足します。
	role, err := serverrole.Parse(string(c.Server.Role))
	if err != nil {
		return fmt.Errorf("SERVER_ROLE: %w", err)
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
	c.Storage.GCSBucket = remoteio.NormalizeBucketName(c.Storage.GCSBucket)
	c.Storage.MusicBucket = remoteio.NormalizeBucketName(c.Storage.MusicBucket)
	c.Auth.AllowedEmails = strlist.Normalize(c.Auth.AllowedEmails)
	c.Auth.AllowedDomains = strlist.Normalize(c.Auth.AllowedDomains)
	c.Auth.AllowedM2MServiceAccounts = strlist.Normalize(c.Auth.AllowedM2MServiceAccounts)
	c.Tasks.AllowedServiceAccounts = strlist.Normalize(c.Tasks.AllowedServiceAccounts)
	// Veo は提供リージョンが限られる（例: us-central1）ため、Cloud Tasks 等と共有する
	// GCP_LOCATION_ID とは別に VEO_LOCATION_ID で上書きできる。未設定なら共通値を使う。
	if c.AI.VeoLocationID == "" {
		c.AI.VeoLocationID = c.GCP.LocationID
	}
	c.NormalizeModels()
	return nil
}

// trimStringValues は、env から読んだ文字列の前後空白を落とします。
//
// 正準形にするのはここ 1 箇所です。使う側で TrimSpace すると、検証は空白付きの値を見て
// 通し、実際に動くのは空白を落とした別の値になります（" " の VEO_OUTPUT_PREFIX が
// 「設定されていません」の検査を通り、出力先だけ gs://<bucket>/jobs/ にずれる、など）。
//
// シークレットは対象外です。鍵の中身を書き換えると、空白込みで運用されている環境の
// セッションが黙って無効になります。
func (c *Config) trimStringValues() {
	for _, value := range []*string{
		&c.Server.ServiceURL,
		&c.Server.Port,
		&c.GCP.ProjectID,
		&c.GCP.LocationID,
		&c.Tasks.QueueID,
		&c.Tasks.TaskAudienceURL,
		&c.Tasks.CallerServiceAccountEmail,
		&c.AI.VeoLocationID,
		&c.AI.VeoOutputPrefix,
		&c.AI.VeoAspectRatio,
		&c.AI.KeyframeImageSize,
		&c.Notification.SlackWebhookURL,
	} {
		*value = strings.TrimSpace(*value)
	}
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

// normalizeWorkerURL normalizes the worker URL or derives it from the service URL.
func normalizeWorkerURL(serviceURL, workerURL string) (string, error) {
	workerURL = strings.TrimSpace(workerURL)
	if workerURL != "" {
		return workerURL, nil
	}
	return joinWorkerPath(serviceURL)
}

// joinWorkerPath returns the default worker endpoint for a service URL.
func joinWorkerPath(serviceURL string) (string, error) {
	serviceURL = strings.TrimSpace(serviceURL)
	if serviceURL == "" {
		return taskGeneratePath, nil
	}
	joined, err := url.JoinPath(serviceURL, taskGeneratePath)
	if err != nil {
		return "", fmt.Errorf("invalid service URL %q: %w", serviceURL, err)
	}
	return joined, nil
}

// GetGCSObjectURL は、指定されたパスから完全なGCSオブジェクトURL ("gs://...") を組み立てます。
func (c *Config) GetGCSObjectURL(path string) string {
	return remoteio.BuildURI(remoteio.SchemeGCS, c.Storage.GCSBucket, path)
}

// TaskCallerServiceAccount は、投入するタスクに指定する caller SA を返します。
// 値は trimStringValues が正準形にしています。
func (c *Config) TaskCallerServiceAccount() string {
	return c.Tasks.CallerServiceAccountEmail
}
