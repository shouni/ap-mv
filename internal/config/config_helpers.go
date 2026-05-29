package config

import (
	"fmt"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-utils/envutil"
	"github.com/shouni/go-utils/text"
	"github.com/shouni/netarmor/securenet"
)

// IsSecureServiceURL は、設定されたServiceURLが安全なスキーム (HTTPS など) を使用しているかどうかを確認します。
func (c *Config) IsSecureServiceURL() bool {
	return securenet.IsSecureServiceURL(c.ServiceURL)
}

// getEnv は環境変数を取得し、存在しない場合はデフォルト値を返します。
func getEnv(key string, defaultValue string) string {
	return envutil.GetEnv(key, defaultValue)
}

// getEnvAsInt は環境変数を整数として取得し、存在しないか変換に失敗した場合はデフォルト値を返します。
func getEnvAsInt(key string, defaultValue int) int {
	return envutil.GetEnvAsInt(key, defaultValue)
}

// parseCommaSeparatedList はカンマ区切りの文字列をパースしてスライスを返します。
func parseCommaSeparatedList(value string) []string {
	return text.ParseCommaSeparatedList(value)
}

// ValidateEssentialConfig はアプリケーション実行に不可欠な設定を検証します。
func (c *Config) ValidateEssentialConfig() error {
	if !c.IsSecureServiceURL() {
		return fmt.Errorf("本番環境では SERVICE_URL ('%s') は HTTPS である必要があります", c.ServiceURL)
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
	if c.GCSBucket == "" {
		return fmt.Errorf("GCS_MUSIC_BUCKET が設定されていません")
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
	return remoteio.BuildGCSURI(c.GCSBucket, path)
}
