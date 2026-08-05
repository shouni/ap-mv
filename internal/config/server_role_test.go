package config

import (
	"testing"
	"time"
)

// newRoleTestConfig は、役割ごとの検証だけを見たいときの土台になる設定を返します。
// Web 面に固有の設定（OAuth・認可リスト）はあえて空にしてあり、
// 各テストが必要に応じて埋めます。
func newRoleTestConfig(role ServerRole) *Config {
	cfg := &Config{}
	cfg.Server.ServiceURL = "https://ap-mv.example.run.app"
	cfg.Server.Role = role
	cfg.Tasks.WorkerURL = "https://ap-mv-worker.example.run.app/tasks/generate"
	cfg.Tasks.QueueID = "mv-queue"
	cfg.Tasks.TaskAudienceURL = "https://ap-mv-worker.example.run.app"
	cfg.GCP.ProjectID = "test-project"
	cfg.GCP.LocationID = "asia-northeast1"
	cfg.GCP.ServiceAccountEmail = "tasks@test-project.iam.gserviceaccount.com"
	cfg.Storage.GCSBucket = "ap-mv"
	cfg.AI.VeoModel = "veo-3.1-generate-001"
	cfg.AI.VeoOutputPrefix = "veo"
	cfg.AI.VeoAspectRatio = "16:9"
	cfg.AI.VeoPollInterval = 5 * time.Second
	cfg.AI.VeoOperationTimeout = 10 * time.Minute
	return cfg
}

func withWebAuth(cfg *Config) *Config {
	cfg.Auth.GoogleClientID = "client-id"
	cfg.Auth.GoogleClientSecret = "client-secret"
	cfg.Auth.SessionSecret = "0123456789abcdef"
	cfg.Auth.SessionEncryptKey = "0123456789abcdef"
	cfg.Auth.AllowedEmails = []string{"user@example.com"}
	return cfg
}

func TestServerRolePredicates(t *testing.T) {
	tests := []struct {
		role       ServerRole
		servesWeb  bool
		servesWork bool
	}{
		{ServerRoleBoth, true, true},
		{ServerRoleWeb, true, false},
		{ServerRoleWorker, false, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := tt.role.ServesWeb(); got != tt.servesWeb {
				t.Fatalf("ServesWeb() = %v, want %v", got, tt.servesWeb)
			}
			if got := tt.role.ServesWorker(); got != tt.servesWork {
				t.Fatalf("ServesWorker() = %v, want %v", got, tt.servesWork)
			}
		})
	}
}

// TestValidateEssentialConfigSkipsWebRequirementsForWorker は、Worker 専用プロセスが
// OAuth 設定なしで起動できることを確認します。これが成り立たないと、
// 使いもしない認証情報へのアクセス権を Worker のサービスアカウントに与える必要が生じます。
func TestValidateEssentialConfigSkipsWebRequirementsForWorker(t *testing.T) {
	cfg := newRoleTestConfig(ServerRoleWorker)

	if err := cfg.ValidateEssentialConfig(); err != nil {
		t.Fatalf("worker role should not require web settings: %v", err)
	}
}

func TestValidateEssentialConfigRequiresWebSettings(t *testing.T) {
	for _, role := range []ServerRole{ServerRoleWeb, ServerRoleBoth} {
		t.Run(string(role), func(t *testing.T) {
			cfg := newRoleTestConfig(role)

			if err := cfg.ValidateEssentialConfig(); err == nil {
				t.Fatal("expected an error when OAuth settings are missing")
			}
			if err := withWebAuth(cfg).ValidateEssentialConfig(); err != nil {
				t.Fatalf("unexpected error once web settings are present: %v", err)
			}
		})
	}
}

// TestValidateEssentialConfigRequiresQueueForBothRoles は、Worker もキューを必要とすることを
// 確認します。ap-comp と違い ap-mv の Worker は動画をカット単位で分割生成し、
// 残りがあれば次のカットを自分で積み直すため、投入側でもあります
// （internal/worker/filter/video_gen.go）。
func TestValidateEssentialConfigRequiresQueueForBothRoles(t *testing.T) {
	for _, role := range []ServerRole{ServerRoleWeb, ServerRoleWorker} {
		t.Run(string(role), func(t *testing.T) {
			cfg := withWebAuth(newRoleTestConfig(role))
			cfg.Tasks.QueueID = ""

			err := cfg.ValidateEssentialConfig()
			if err == nil {
				t.Fatal("expected an error when the queue is missing")
			}
		})
	}
}

// TestValidateEssentialConfigRequiresTaskAudienceForWorker は、Worker が
// audience 未設定のまま起動しないことを確認します。未設定だと OIDC 検証器が
// fail-closed になり、全タスクが 500 で失敗し続けます。
func TestValidateEssentialConfigRequiresTaskAudienceForWorker(t *testing.T) {
	cfg := newRoleTestConfig(ServerRoleWorker)
	cfg.Tasks.TaskAudienceURL = ""

	if err := cfg.ValidateEssentialConfig(); err == nil {
		t.Fatal("expected an error when TASK_AUDIENCE_URL is missing")
	}
}
