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

// TestParseServerRole は、SERVER_ROLE の明示を必須にしていることを確認します。
//
// 未設定が both に落ちると、本番の環境変数が 1 つ欠けただけで公開 web に
// /tasks/generate が復活します。ここが退行すると、その設定漏れが黙って通ります。
func TestParseServerRole(t *testing.T) {
	t.Run("有効な値", func(t *testing.T) {
		tests := []struct {
			raw  string
			want ServerRole
		}{
			{raw: "web", want: ServerRoleWeb},
			{raw: "worker", want: ServerRoleWorker},
			{raw: "both", want: ServerRoleBoth},
			// 大文字と前後の空白は正規化して受け付ける。
			{raw: " WEB ", want: ServerRoleWeb},
		}

		for _, tt := range tests {
			got, err := ParseServerRole(tt.raw)
			if err != nil {
				t.Fatalf("ParseServerRole(%q) error = %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("ParseServerRole(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		}
	})

	t.Run("空文字と未知の値はエラー", func(t *testing.T) {
		for _, raw := range []string{"", "   ", "wrker", "all", "true"} {
			if _, err := ParseServerRole(raw); err == nil {
				t.Errorf("ParseServerRole(%q) が受理されている", raw)
			}
		}
	})
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

// TestTaskIssuersFallsBackToServiceAccountEmail は、ALLOWED_TASK_SERVICE_ACCOUNTS を
// 設定していない既存のデプロイがそのまま動き続けることを確認します。
func TestTaskIssuersFallsBackToServiceAccountEmail(t *testing.T) {
	cfg := newRoleTestConfig(ServerRoleWorker)

	got := cfg.TaskIssuers()
	if len(got) != 1 || got[0] != cfg.GCP.ServiceAccountEmail {
		t.Fatalf("TaskIssuers() = %v, want [%s]", got, cfg.GCP.ServiceAccountEmail)
	}
}

// TestTaskIssuersPrefersExplicitAllowlist は、web と worker で実行サービスアカウントを
// 分けたときに発行元を 2 つ受け付けられることを確認します。ap-mv は worker も
// 継続カットを投入するため、発行元が 1 つに収まりません。
func TestTaskIssuersPrefersExplicitAllowlist(t *testing.T) {
	cfg := newRoleTestConfig(ServerRoleWorker)
	cfg.Tasks.AllowedServiceAccounts = []string{
		"ap-mv-web-runner@test-project.iam.gserviceaccount.com",
		"ap-mv-worker-runner@test-project.iam.gserviceaccount.com",
	}

	got := cfg.TaskIssuers()
	if len(got) != 2 {
		t.Fatalf("TaskIssuers() = %v, want the two configured issuers", got)
	}
	for _, issuer := range got {
		if issuer == cfg.GCP.ServiceAccountEmail {
			t.Fatalf("TaskIssuers() must not fall back when an allowlist is configured: %v", got)
		}
	}
}

// TestValidateEssentialConfigRequiresTaskIssuerForWorker は、発行元が 1 件も無いまま
// Worker が起動しないことを確認します。空だと検証器が fail-closed になり、
// 全タスクが 500 で失敗し続けます。
func TestValidateEssentialConfigRequiresTaskIssuerForWorker(t *testing.T) {
	cfg := newRoleTestConfig(ServerRoleWorker)
	cfg.GCP.ServiceAccountEmail = ""
	cfg.Tasks.AllowedServiceAccounts = nil

	if err := cfg.ValidateEssentialConfig(); err == nil {
		t.Fatal("expected an error when no task issuer is configured")
	}
}
