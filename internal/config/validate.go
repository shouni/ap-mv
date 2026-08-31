package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/shouni/netarmor/securenet"

	"github.com/shouni/ap-mv/internal/domain"
)

// IsSecureServiceURL は、設定されたServiceURLが安全なスキーム (HTTPS など) を使用しているかどうかを確認します。
func (c *Config) IsSecureServiceURL() bool {
	return securenet.IsSecureServiceURL(c.Server.ServiceURL)
}

// IsSecureWorkerURL は、Cloud Tasks の配送先URLが安全なスキームを使用しているか確認します。
func (c *Config) IsSecureWorkerURL() bool {
	return securenet.IsSecureServiceURL(c.Tasks.WorkerURL)
}

// ValidateEssentialConfig はアプリケーション実行に不可欠な設定を検証します。
func (c *Config) ValidateEssentialConfig() error {
	if !c.IsSecureServiceURL() {
		return fmt.Errorf("本番環境では SERVICE_URL ('%s') は HTTPS である必要があります", c.Server.ServiceURL)
	}
	if c.GCP.ProjectID == "" {
		return fmt.Errorf("GCP_PROJECT_ID が設定されていません (Vertex AI 運用に必須)")
	}
	if c.GCP.LocationID == "" {
		return fmt.Errorf("GCP_LOCATION_ID が設定されていません (デフォルト: asia-northeast1)")
	}
	if c.TaskCallerServiceAccount() == "" {
		return fmt.Errorf("TASK_CALLER_SERVICE_ACCOUNT_EMAIL が設定されていません")
	}
	// ap-music と違い、ap-mv は Worker 側もタスクを投入する。動画をカット単位で
	// 分割生成し、残りがあれば次のカットを自分で積み直すため
	// （internal/worker/filter/video_gen.go）。したがってキューと WORKER_URL は
	// どちらの役割でも必須になる。
	if c.Tasks.QueueID == "" {
		return fmt.Errorf("CLOUD_TASKS_QUEUE_ID が設定されていません")
	}
	// 未設定を SERVICE_URL から導出するのは worker 面を担うプロセスだけなので、web 専用
	// プロセスではここが空のまま残る。通すと配送先なしでタスクを投入しようとする。
	if c.Tasks.WorkerURL == "" {
		return fmt.Errorf("WORKER_URL が設定されていません（worker サービスの URL を渡します）")
	}
	if !c.IsSecureWorkerURL() {
		return fmt.Errorf("本番環境では WORKER_URL ('%s') は HTTPS である必要があります", c.Tasks.WorkerURL)
	}
	// 正規化は normalize() が済ませています（検証は設定を書き換えません）。
	if c.Storage.GCSBucket == "" {
		return fmt.Errorf("AP_MV_BUCKET が設定されていません")
	}
	// モデル一覧は役割を問わず必須です。worker は生成に、web は投稿フォームの選択肢に
	// 使うためで、片方だけを検証するともう一方が空のまま起動してしまいます。
	for _, m := range []struct {
		envKey string
		models []string
	}{
		{"GEMINI_MODELS", c.AI.GeminiModels},
		{"IMAGE_MODELS", c.AI.ImageModels},
		{"VEO_MODELS", c.AI.VeoModels},
	} {
		if len(m.models) == 0 {
			return fmt.Errorf("%s が設定されていません（カンマ区切りで複数指定すると、先頭が既定でフォームの選択肢になります）", m.envKey)
		}
	}
	if c.AI.VeoOutputPrefix == "" {
		return fmt.Errorf("VEO_OUTPUT_PREFIX が設定されていません")
	}
	if !domain.IsAllowedAspectRatio(c.AI.VeoAspectRatio) {
		return fmt.Errorf("VEO_ASPECT_RATIO は %s のいずれかである必要があります", strings.Join(domain.AllowedAspectRatios, " / "))
	}
	if c.AI.VeoPollInterval <= 0 {
		return fmt.Errorf("VEO_POLL_INTERVAL は正の duration である必要があります")
	}
	if c.AI.VeoOperationTimeout <= 0 {
		return fmt.Errorf("VEO_OPERATION_TIMEOUT は正の duration である必要があります")
	}
	// go-veo-orchestrator は画作りの既定値を持たないため、ここが唯一の出所です。
	// 不正値を黙って通すと、モデルが選んだ解像度で焼かれて誰も気付きません。
	if !domain.IsAllowedImageSize(c.AI.KeyframeImageSize) {
		return fmt.Errorf("KEYFRAME_IMAGE_SIZE は %s のいずれかである必要があります", strings.Join(domain.AllowedImageSizes, " / "))
	}
	c.warnContradictoryKeyframeThroughput()

	if c.Server.Role.ServesWeb() {
		if err := c.validateWebConfig(); err != nil {
			return err
		}
	}

	if c.Server.Role.ServesWorker() {
		if c.Tasks.TaskAudienceURL == "" {
			return fmt.Errorf("TASK_AUDIENCE_URL が設定されていません。Cloud Tasks の OIDC 検証に必須です")
		}
		// 空だと検証器が fail-closed になり、全タスクが 500 で失敗し続けます。
		if len(c.Tasks.AllowedServiceAccounts) == 0 {
			return fmt.Errorf("許可する caller SA が 1 件も指定されていません。ALLOWED_TASK_SERVICE_ACCOUNTS を設定してください")
		}
		if err := c.validatePipelineTimeout(); err != nil {
			return err
		}
	}

	return nil
}

// validatePipelineTimeout は、パイプラインを走らせる役割で PIPELINE_TIMEOUT が
// TASK_DISPATCH_DEADLINE より確実に短いことを検査します。
//
// アプリが自分で先に諦めることが要点です。逆順（等号を含む）だと Cloud Tasks が先に
// リクエストを打ち切り、失敗の記録も Slack 通知も走らないまま、キューは max_attempts = 1 で
// 再試行しないため、タスクが running のまま残ります。無制限（0 以下）も同じ理由で拒みます。
// 打ち切りの実効上限は 3 つのうち最小値なので、この 1 本だけは起動時に強制します。
func (c *Config) validatePipelineTimeout() error {
	if c.Tasks.DispatchDeadline <= 0 {
		return fmt.Errorf("TASK_DISPATCH_DEADLINE が設定されていません（三段のタイムアウトはデプロイ設定が決めます。例: 30m）")
	}
	if c.Tasks.PipelineTimeout <= 0 {
		return fmt.Errorf("PIPELINE_TIMEOUT は worker では無制限にできません。Cloud Tasks の打ち切り（%s）より短い値を設定してください", c.Tasks.DispatchDeadline)
	}
	if c.Tasks.PipelineTimeout >= c.Tasks.DispatchDeadline {
		return fmt.Errorf("PIPELINE_TIMEOUT (%s) は Cloud Tasks の打ち切り (%s) より短くしてください。等号でもアプリが失敗を記録する前に打ち切られます", c.Tasks.PipelineTimeout, c.Tasks.DispatchDeadline)
	}
	return nil
}

// warnContradictoryKeyframeThroughput は、並列度と発射間隔を両方上げた設定を警告します。
//
// go-veo-orchestrator は 1 つのリミッターで AI 呼び出しの間隔を空けるため、スループットは
// 並列度によらず 1/KEYFRAME_RATE_INTERVAL で頭打ちになります。並列度だけ上げても速く
// ならず、しかもエラーにはならないので、設定した側は効いているつもりのまま待ち続けます。
// 起動時に実効レートを出して、その取り違えに気付けるようにします。
func (c *Config) warnContradictoryKeyframeThroughput() {
	if c.AI.KeyframeMaxConcurrency <= 1 || c.AI.KeyframeRateInterval <= 0 {
		return
	}
	slog.Warn("KEYFRAME_MAX_CONCURRENCY は KEYFRAME_RATE_INTERVAL に頭打ちにされます",
		"keyframe_max_concurrency", c.AI.KeyframeMaxConcurrency,
		"keyframe_rate_interval", c.AI.KeyframeRateInterval,
		"effective_images_per_minute", int(time.Minute/c.AI.KeyframeRateInterval))
}

// validateWebConfig は Web 面（OAuth ログインとセッション）に必要な設定を検証します。
// Worker 面だけを提供するプロセスに OAuth 関連の設定を要求すると、
// 使わない認証情報へのアクセス権を配ることになるため役割で分けています。
func (c *Config) validateWebConfig() error {
	if c.Auth.GoogleClientID == "" || c.Auth.GoogleClientSecret == "" || c.Auth.SessionSecret == "" {
		return fmt.Errorf("google OAuth 関連の設定（ClientID, ClientSecret, SessionSecret）が不足しています")
	}

	if len(c.Auth.AllowedEmails) == 0 && len(c.Auth.AllowedDomains) == 0 {
		return fmt.Errorf("許可されたメールアドレスまたはドメインが一つも設定されていません（認可リストが空です）")
	}

	if c.Auth.SessionEncryptKey == "" {
		return fmt.Errorf("SESSION_ENCRYPT_KEY が設定されていません。セキュアな運用のために必須です")
	}

	// SessionEncryptKey の長さチェック (AES要件: 16, 24, 32 bytes)
	keyLen := len(c.Auth.SessionEncryptKey)
	if keyLen != 16 && keyLen != 24 && keyLen != 32 {
		return fmt.Errorf("SESSION_ENCRYPT_KEY の長さが不正です (%d バイト)。16, 24, 32 バイトのいずれかにしてください", keyLen)
	}

	return nil
}
