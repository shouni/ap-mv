package config

import (
	"fmt"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/netarmor/securenet"
)

// IsSecureServiceURL は、設定されたServiceURLが安全なスキーム (HTTPS など) を使用しているかどうかを確認します。
func (c *Config) IsSecureServiceURL() bool {
	return securenet.IsSecureServiceURL(c.ServiceURL)
}

// IsSecureWorkerURL は、Cloud Tasks の配送先URLが安全なスキームを使用しているか確認します。
func (c *Config) IsSecureWorkerURL() bool {
	return securenet.IsSecureServiceURL(c.WorkerURL)
}

func normalizeGCSBucket(bucket string) string {
	bucket = strings.TrimSpace(bucket)
	bucket = strings.TrimPrefix(bucket, "gs://")
	return strings.Trim(bucket, "/")
}

func normalizeWorkerURL(serviceURL, workerURL string) string {
	workerURL = strings.TrimSpace(workerURL)
	if workerURL != "" {
		return workerURL
	}
	serviceURL = strings.TrimRight(strings.TrimSpace(serviceURL), "/")
	if serviceURL == "" {
		return "/tasks/generate"
	}
	return serviceURL + "/tasks/generate"
}

// ValidateEssentialConfig はアプリケーション実行に不可欠な設定を検証します。
func (c *Config) ValidateEssentialConfig() error {
	if !c.IsSecureServiceURL() {
		return fmt.Errorf("本番環境では SERVICE_URL ('%s') は HTTPS である必要があります", c.ServiceURL)
	}
	if !c.IsSecureWorkerURL() {
		return fmt.Errorf("本番環境では WORKER_URL ('%s') は HTTPS である必要があります", c.WorkerURL)
	}

	if c.GoogleClientID == "" || c.GoogleClientSecret == "" || c.SessionSecret == "" {
		return fmt.Errorf("Google OAuth 関連の設定（ClientID, ClientSecret, SessionSecret）が不足しています")
	}

	if len(c.AllowedEmails) == 0 && len(c.AllowedDomains) == 0 {
		return fmt.Errorf("許可されたメールアドレスまたはドメインが一つも設定されていません（認可リストが空です）")
	}

	if c.ProjectID == "" {
		return fmt.Errorf("GCP_PROJECT_ID が設定されていません (Vertex AI 運用に必須)")
	}
	if c.LocationID == "" {
		return fmt.Errorf("GCP_LOCATION_ID が設定されていません (デフォルト: asia-northeast1)")
	}
	if c.QueueID == "" {
		return fmt.Errorf("CLOUD_TASKS_QUEUE_ID が設定されていません")
	}
	if c.ServiceAccountEmail == "" {
		return fmt.Errorf("SERVICE_ACCOUNT_EMAIL が設定されていません")
	}
	c.GCSBucket = normalizeGCSBucket(c.GCSBucket)
	if c.GCSBucket == "" {
		return fmt.Errorf("GCS_MUSIC_BUCKET が設定されていません")
	}
	if c.VeoModel == "" {
		return fmt.Errorf("VEO_MODEL が設定されていません")
	}
	if c.VeoOutputPrefix == "" {
		return fmt.Errorf("VEO_OUTPUT_PREFIX が設定されていません")
	}
	if c.VeoAspectRatio != "16:9" && c.VeoAspectRatio != "9:16" {
		return fmt.Errorf("VEO_ASPECT_RATIO は 16:9 または 9:16 である必要があります")
	}
	if c.VeoPollInterval <= 0 {
		return fmt.Errorf("VEO_POLL_INTERVAL は正の duration である必要があります")
	}
	if c.VeoOperationTimeout <= 0 {
		return fmt.Errorf("VEO_OPERATION_TIMEOUT は正の duration である必要があります")
	}

	if c.SessionEncryptKey == "" {
		return fmt.Errorf("SESSION_ENCRYPT_KEY が設定されていません。セキュアな運用のために必須です")
	}

	// SessionEncryptKey の長さチェック (AES要件: 16, 24, 32 bytes)
	keyLen := len(c.SessionEncryptKey)
	if keyLen != 16 && keyLen != 24 && keyLen != 32 {
		return fmt.Errorf("SESSION_ENCRYPT_KEY の長さが不正です (%d バイト)。16, 24, 32 バイトのいずれかにしてください", keyLen)
	}

	return nil
}

// GetGCSObjectURL は、指定されたパスから完全なGCSオブジェクトURL ("gs://...") を組み立てます。
func (c *Config) GetGCSObjectURL(path string) string {
	return remoteio.BuildGCSURI(normalizeGCSBucket(c.GCSBucket), path)
}
