package builder

import (
	"context"
	"testing"

	"github.com/shouni/gcp-kit/auth/oidc"
	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/gcp-kit/worker"
	"github.com/shouni/go-serve-kit/serverrole"

	"github.com/shouni/ap-mv/internal/app"
	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// stubPipeline は、役割ごとの組み立てだけを見るための ports.Pipeline スタブです。
type stubPipeline struct{}

func (stubPipeline) Execute(context.Context, domain.Task) error { return nil }

func (stubPipeline) Close() error { return nil }

var _ ports.Pipeline = stubPipeline{}

// newRoleTestConfig は、SERVER_ROLE の分岐だけを見るための最小構成を返します。
func newRoleTestConfig(role serverrole.Role) *config.Config {
	cfg := &config.Config{}
	cfg.Server.Role = role
	cfg.Server.ServiceURL = "https://web.example.test"
	// Web 面は M2M 検証器の構成に許可リストを要求します（newM2MVerifier 参照）。
	cfg.Auth.AllowedM2MServiceAccounts = []string{"ap-mcp-runner@test-project.iam.gserviceaccount.com"}
	cfg.Tasks.CallerServiceAccountEmail = "caller@test-project.iam.gserviceaccount.com"
	cfg.Tasks.AllowedServiceAccounts = []string{"web-runner@test-project.iam.gserviceaccount.com"}
	cfg.Tasks.TaskAudienceURL = "https://worker.example.test"
	cfg.Auth.GoogleClientID = "test-client-id"
	cfg.Auth.GoogleClientSecret = "test-client-secret"
	cfg.Auth.AllowedEmails = []string{"someone@example.test"}
	cfg.NormalizeModels()
	return cfg
}

// BuildHandlers が SERVER_ROLE に応じて、担当しない面のハンドラーを nil のままにすること。
//
// router.go は「nil のハンドラーはルート登録ごと省く」ことで役割を分けているため、
// ここで nil にし損ねると、web サービス上に /tasks/generate が生えたり、worker が
// OAuth クライアントを抱えたりします。どちらもルーターのテストからは見えません。
func TestBuildHandlersWiresOnlyTheRolesPlane(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		role       serverrole.Role
		wantWeb    bool
		wantWorker bool
	}{
		{name: "web", role: serverrole.Web, wantWeb: true, wantWorker: false},
		{name: "worker", role: serverrole.Worker, wantWeb: false, wantWorker: true},
		{name: "both", role: serverrole.Both, wantWeb: true, wantWorker: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			appCtx := &app.Container{SessionStore: session.NewMemoryStore(), Config: newRoleTestConfig(tt.role), Pipeline: stubPipeline{}}
			h, err := BuildHandlers(appCtx)
			if err != nil {
				t.Fatalf("BuildHandlers() error = %v", err)
			}

			if got := h.Web != nil; got != tt.wantWeb {
				t.Errorf("Web != nil = %v, want %v", got, tt.wantWeb)
			}
			// Auth と M2M は Web 面の入口です。worker で組むと、使わない OAuth
			// クライアントシークレットへのアクセス権を配ることになります。
			if got := h.Auth != nil; got != tt.wantWeb {
				t.Errorf("Auth != nil = %v, want %v", got, tt.wantWeb)
			}
			if got := h.M2M != nil; got != tt.wantWeb {
				t.Errorf("M2M != nil = %v, want %v", got, tt.wantWeb)
			}

			if got := h.Worker != nil; got != tt.wantWorker {
				t.Errorf("Worker != nil = %v, want %v", got, tt.wantWorker)
			}
			if got := h.TaskAuth != nil; got != tt.wantWorker {
				t.Errorf("TaskAuth != nil = %v, want %v", got, tt.wantWorker)
			}
		})
	}
}

// Web 面だけを担うプロセスは Pipeline を持たずに組み立てられること。
// BuildContainer が role=web で生成系（Vertex AI・Veo・Slack）を組まないことの対です。
func TestBuildHandlersWebRoleNeedsNoPipeline(t *testing.T) {
	t.Parallel()

	appCtx := &app.Container{SessionStore: session.NewMemoryStore(), Config: newRoleTestConfig(serverrole.Web)}
	h, err := BuildHandlers(appCtx)
	if err != nil {
		t.Fatalf("BuildHandlers() error = %v", err)
	}
	if h.Web == nil {
		t.Fatal("Pipeline が無いだけで Web ハンドラーが組み立てられていない")
	}
}

// Worker 面を担うのに Cloud Tasks の OIDC 検証を構成できない場合は起動を止めること。
// 検証器は fail-closed なので、そのまま起動すると全タスクが失敗し続けます。
func TestBuildHandlersFailsWhenWorkerCannotVerifyTasks(t *testing.T) {
	t.Parallel()

	cfg := newRoleTestConfig(serverrole.Worker)
	cfg.Tasks.TaskAudienceURL = ""

	appCtx := &app.Container{SessionStore: session.NewMemoryStore(), Config: cfg, Pipeline: stubPipeline{}}
	if _, err := BuildHandlers(appCtx); err == nil {
		t.Fatal("TASK_AUDIENCE_URL が無いのに BuildHandlers() が成功している")
	}
}

// TaskAuth と Worker の片方だけが構成された形を Validate が弾くこと。
//
// router.go は TaskAuth の有無だけを見てルート登録を省くため、この不整合を通すと
// /tasks/generate が黙って 404 になります。設定漏れなのか実装バグなのかが
// リクエストからは区別できないので、起動時に落とします。
func TestAppHandlersValidateRejectsHalfConfiguredWorker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		h       *AppHandlers
		wantErr bool
	}{
		{name: "どちらも nil (web ロール)", h: &AppHandlers{}},
		{
			name:    "TaskAuth だけある",
			h:       &AppHandlers{TaskAuth: mustOIDC(t, "https://worker.example.test", []string{"runner@example.iam.gserviceaccount.com"})},
			wantErr: true,
		},
		{
			name:    "Worker だけある",
			h:       &AppHandlers{Worker: worker.NewHandler[domain.Task](stubPipeline{})},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.h.Validate()
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// mustOIDC は、テスト用に構成済みの検証器を作ります（New は設定が欠けるとエラーを返します）。
func mustOIDC(t *testing.T, audience string, allowed []string) *oidc.Verifier {
	t.Helper()
	v, err := oidc.New(audience, allowed)
	if err != nil {
		t.Fatalf("oidc.New() error = %v", err)
	}
	return v
}
