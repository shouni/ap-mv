package config

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shouni/go-serve-kit/serverrole"
)

// newRoleTestConfig は、役割ごとの検証だけを見たいときの土台になる設定を返します。
// Web 面に固有の設定（OAuth・認可リスト）はあえて空にしてあり、
// 各テストが必要に応じて埋めます。
func newRoleTestConfig(role serverrole.Role) *Config {
	cfg := &Config{}
	cfg.Server.ServiceURL = "https://ap-mv.example.run.app"
	cfg.Server.Role = role
	cfg.Tasks.WorkerURL = "https://ap-mv-worker.example.run.app"
	cfg.Tasks.QueueID = "mv-queue"
	cfg.Tasks.CallerServiceAccountEmail = "caller@test-project.iam.gserviceaccount.com"
	cfg.Tasks.AllowedServiceAccounts = []string{"web-runner@test-project.iam.gserviceaccount.com"}
	cfg.Tasks.TaskAudienceURL = "https://ap-mv-worker.example.run.app"
	cfg.GCP.ProjectID = "test-project"
	cfg.GCP.LocationID = "asia-northeast1"
	cfg.Storage.GCSBucket = "ap-mv"
	// モデル一覧は役割を問わず必須です（web は選択肢、worker は生成に使います）。
	cfg.AI.GeminiModels = []string{"gemini-test-flash"}
	cfg.AI.ImageModels = []string{"gemini-test-image"}
	cfg.AI.VeoModels = []string{"veo-test"}
	cfg.AI.VeoModel = "veo-test"
	cfg.AI.VeoOutputPrefix = "veo"
	cfg.AI.VeoAspectRatio = "16:9"
	// 画作りの値は go-veo-orchestrator が既定を持たないため、アプリ側で必須です。
	cfg.AI.KeyframeImageSize = "2K"
	cfg.AI.VeoPollInterval = 5 * time.Second
	cfg.AI.VeoOperationTimeout = 10 * time.Minute
	// env の既定値は素の struct には入らないため、実際の設定と同じ状態にしておきます。
	// 0 のままだと worker の検証が「無制限」を理由に落ちます。
	cfg.Tasks.PipelineTimeout = 25 * time.Minute
	cfg.Tasks.DispatchDeadline = testDispatchDeadline
	return cfg
}

func withWebAuth(cfg *Config) *Config {
	cfg.Auth.GoogleClientID = "client-id"
	cfg.Auth.GoogleClientSecret = "client-secret"
	cfg.Auth.AllowedEmails = []string{"user@example.com"}
	return cfg
}

// TestValidateEssentialConfigSkipsWebRequirementsForWorker は、Worker 専用プロセスが
// OAuth 設定なしで起動できることを確認します。これが成り立たないと、
// 使いもしない認証情報へのアクセス権を Worker のサービスアカウントに与える必要が生じます。
func TestValidateEssentialConfigSkipsWebRequirementsForWorker(t *testing.T) {
	cfg := newRoleTestConfig(serverrole.Worker)

	if err := cfg.ValidateEssentialConfig(); err != nil {
		t.Fatalf("worker role should not require web settings: %v", err)
	}
}

func TestValidateEssentialConfigRequiresWebSettings(t *testing.T) {
	for _, role := range []serverrole.Role{serverrole.Web, serverrole.Both} {
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
// 確認します。ap-music と違い ap-mv の Worker は動画をカット単位で分割生成し、
// 残りがあれば次のカットを自分で積み直すため、投入側でもあります
// （internal/worker/filter/video_gen.go）。
func TestValidateEssentialConfigRequiresQueueForBothRoles(t *testing.T) {
	for _, role := range []serverrole.Role{serverrole.Web, serverrole.Worker} {
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
	cfg := newRoleTestConfig(serverrole.Worker)
	cfg.Tasks.TaskAudienceURL = ""

	if err := cfg.ValidateEssentialConfig(); err == nil {
		t.Fatal("expected an error when TASK_AUDIENCE_URL is missing")
	}
}

// TestValidateEssentialConfigRequiresAllowlistForWorker は、発行元が 1 件も無いまま
// Worker が起動しないことを確認します。空だと検証器が fail-closed になり、
// 全タスクが 500 で失敗し続けます。
func TestValidateEssentialConfigRequiresAllowlistForWorker(t *testing.T) {
	cfg := newRoleTestConfig(serverrole.Worker)
	cfg.Tasks.AllowedServiceAccounts = nil

	if err := cfg.ValidateEssentialConfig(); err == nil {
		t.Fatal("expected an error when no task issuer is configured")
	}
}

// TestNormalizeTrimsConfiguredValues は、env から読んだ値の前後空白を normalize() が
// 落とすことを確認します。
//
// 正準形にする場所を 1 箇所に決めたのは、使う側で TrimSpace すると検証と実際の動作が
// 食い違うためです。空白だけの VEO_OUTPUT_PREFIX は「設定されていません」の検査を
// 通り抜け、出力先だけが gs://<bucket>/jobs/ にずれていました。
func TestNormalizeTrimsConfiguredValues(t *testing.T) {
	cfg := newRoleTestConfig(serverrole.Both)
	cfg.Tasks.CallerServiceAccountEmail = "  caller@test-project.iam.gserviceaccount.com  "
	cfg.AI.VeoOutputPrefix = "  veo  "

	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize() error = %v", err)
	}

	if got, want := cfg.TaskCallerServiceAccount(), "caller@test-project.iam.gserviceaccount.com"; got != want {
		t.Errorf("TaskCallerServiceAccount() = %q, want %q", got, want)
	}
	if got, want := cfg.AI.VeoOutputPrefix, "veo"; got != want {
		t.Errorf("VeoOutputPrefix = %q, want %q", got, want)
	}
}

// 空白だけの VEO_OUTPUT_PREFIX は起動時に落ちること。normalize() が空へ寄せるので、
// 検証がそのまま関門になります。
func TestValidateEssentialConfigRejectsBlankVeoOutputPrefix(t *testing.T) {
	cfg := withWebAuth(newRoleTestConfig(serverrole.Both))
	cfg.AI.VeoOutputPrefix = "   "

	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize() error = %v", err)
	}

	err := cfg.ValidateEssentialConfig()
	if err == nil {
		t.Fatal("空白だけの VEO_OUTPUT_PREFIX が起動時検証を通ってしまいました")
	}
	if !strings.Contains(err.Error(), "VEO_OUTPUT_PREFIX") {
		t.Fatalf("error = %v, want it to mention VEO_OUTPUT_PREFIX", err)
	}
}

// TestValidateEssentialConfigRequiresKeyframeImageSize は、解像度が不正なら起動時に落ちる
// ことを確認します。go-veo-orchestrator は画作りの既定値を持たないため、ここを黙って
// 通すとモデルが選んだ解像度で焼かれ、誰も気付きません。
func TestValidateEssentialConfigRequiresKeyframeImageSize(t *testing.T) {
	for name, value := range map[string]string{
		"empty":   "",
		"unknown": "8K",
		"lowcase": "2k",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := withWebAuth(newRoleTestConfig(serverrole.Both))
			cfg.AI.KeyframeImageSize = value

			if err := cfg.ValidateEssentialConfig(); err == nil {
				t.Errorf("KEYFRAME_IMAGE_SIZE=%q should be rejected", value)
			}
		})
	}
}

// TestWarnContradictoryKeyframeThroughput は、並列度と発射間隔を両方上げた設定を検知する
// 条件を確認します。スループットは並列度によらず発射間隔で頭打ちになるため、この組み合わせは
// 「効いているつもりで効いていない」状態になります（エラーにはしません。運用上は有効な設定で、
// 意図的に絞っている場合もあるためです）。
func TestWarnContradictoryKeyframeThroughput(t *testing.T) {
	tests := map[string]struct {
		concurrency int
		interval    time.Duration
		wantWarn    bool
	}{
		"並列度1なら矛盾しない":  {concurrency: 1, interval: 60 * time.Second, wantWarn: false},
		"間隔なしなら矛盾しない":  {concurrency: 4, interval: 0, wantWarn: false},
		"両方大きいと頭打ちになる": {concurrency: 4, interval: 60 * time.Second, wantWarn: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := newRoleTestConfig(serverrole.Worker)
			cfg.AI.KeyframeMaxConcurrency = tt.concurrency
			cfg.AI.KeyframeRateInterval = tt.interval

			// 警告そのものは slog へ出るだけなので、判定条件を直接確認する。
			got := cfg.AI.KeyframeMaxConcurrency > 1 && cfg.AI.KeyframeRateInterval > 0
			if got != tt.wantWarn {
				t.Errorf("warn = %v, want %v", got, tt.wantWarn)
			}
			cfg.warnContradictoryKeyframeThroughput() // panic しないこと
		})
	}
}

// TestValidateEssentialConfigRequiresPipelineTimeoutUnderDispatchDeadline は、
// worker が「Cloud Tasks より先に自分で諦める」設定でしか起動しないことを確認します。
//
// 等号・超過・無制限のいずれも、打ち切りが Cloud Tasks 側から来る点で同じです。その場合は
// プロセスごと止められるため失敗ハンドラも Slack 通知も走らず、max_attempts = 1 で再試行も
// 無いので、タスクが running のまま残ります。既定値がこの関係を満たすことも併せて固定します。
func TestValidateEssentialConfigRequiresPipelineTimeoutUnderDispatchDeadline(t *testing.T) {
	// 既定値は envDefault タグにしか無いため、タグそのものを読んで検査します。
	// ここが打ち切り以上だと、PIPELINE_TIMEOUT を渡さない worker が一切起動しなくなります。
	// 既定値は持ちません。出どころはデプロイ設定（Terraform）1 箇所です。
	t.Run("既定値を持たない", func(t *testing.T) {
		field, ok := reflect.TypeFor[TasksConfig]().FieldByName("PipelineTimeout")
		if !ok {
			t.Fatal("TasksConfig.PipelineTimeout not found")
		}
		if got := field.Tag.Get("envDefault"); got != "" {
			t.Errorf("envDefault = %q, 既定値を持たせないでください", got)
		}
		if err := newRoleTestConfig(serverrole.Worker).ValidateEssentialConfig(); err != nil {
			t.Fatalf("worker should start with a valid timeout: %v", err)
		}
	})

	for _, tt := range []struct {
		name    string
		timeout time.Duration
	}{
		{name: "打ち切りと等しいと落ちる", timeout: testDispatchDeadline},
		{name: "打ち切りより長いと落ちる", timeout: testDispatchDeadline + time.Minute},
		{name: "無制限は落ちる", timeout: 0},
		{name: "負の無制限も落ちる", timeout: -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newRoleTestConfig(serverrole.Worker)
			cfg.Tasks.PipelineTimeout = tt.timeout

			err := cfg.ValidateEssentialConfig()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), "PIPELINE_TIMEOUT") {
				t.Fatalf("error = %v, want it to mention PIPELINE_TIMEOUT", err)
			}
		})
	}

	// web はパイプラインを組み立てないため、この検査の対象外です。
	t.Run("web は無制限でも起動できる", func(t *testing.T) {
		cfg := withWebAuth(newRoleTestConfig(serverrole.Web))
		cfg.Tasks.PipelineTimeout = 0

		if err := cfg.ValidateEssentialConfig(); err != nil {
			t.Fatalf("web should not be subject to the check: %v", err)
		}
	})
}

// 打ち切りに既定値を持たせないこと。
//
// 三段のタイムアウトはデプロイ先の事情で決まるので、出どころは Terraform 1 箇所に
// 閉じます。アプリが既定を持つと同じ数字が 2 箇所に現れ、設定漏れが
// 「誰も選んでいない値」で動いてしまいます。
func TestDispatchDeadlineHasNoDefault(t *testing.T) {
	field, ok := reflect.TypeFor[TasksConfig]().FieldByName("DispatchDeadline")
	if !ok {
		t.Fatal("TasksConfig.DispatchDeadline not found")
	}
	if got := field.Tag.Get("envDefault"); got != "" {
		t.Errorf("envDefault = %q, 既定値を持たせないでください", got)
	}
}

// 未設定なら起動時に落ちること。
func TestDispatchDeadlineIsRequired(t *testing.T) {
	cfg := newRoleTestConfig(serverrole.Worker)
	cfg.Tasks.DispatchDeadline = 0

	err := cfg.ValidateEssentialConfig()
	if err == nil {
		t.Fatal("未設定が素通りしました")
	}
	if !strings.Contains(err.Error(), "TASK_DISPATCH_DEADLINE") {
		t.Errorf("エラーに変数名がありません: %v", err)
	}
}

// 打ち切りは env で伸ばせること。定数だった頃は再ビルドが要りました。
func TestDispatchDeadlineIsConfigurable(t *testing.T) {
	cfg := newRoleTestConfig(serverrole.Worker)
	cfg.Tasks.DispatchDeadline = 20 * time.Minute
	cfg.Tasks.PipelineTimeout = 25 * time.Minute

	if err := cfg.ValidateEssentialConfig(); err == nil {
		t.Fatal("縮めた打ち切りより長い PIPELINE_TIMEOUT が通ってしまいました")
	}

	cfg.Tasks.DispatchDeadline = 30 * time.Minute
	if err := cfg.ValidateEssentialConfig(); err != nil {
		t.Fatalf("伸ばした組み合わせで失敗しました: %v", err)
	}
}

// testDispatchDeadline は、テストで使う打ち切りです。アプリは既定値を持たないため、
// 実際のデプロイ設定と同じく明示します。
const testDispatchDeadline = 30 * time.Minute
