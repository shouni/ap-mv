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

// stubPipeline is a ports.Pipeline stub for looking only at per-role assembly.
type stubPipeline struct{}

func (stubPipeline) Execute(context.Context, domain.Task) error { return nil }

func (stubPipeline) Close() error { return nil }

var _ ports.Pipeline = stubPipeline{}

// newRoleTestConfig returns the minimal config for looking only at the SERVER_ROLE branch.
func newRoleTestConfig(role serverrole.Role) *config.Config {
	cfg := &config.Config{}
	cfg.Server.Role = role
	cfg.Server.ServiceURL = "https://web.example.test"
	// The web face requires an allowlist to configure the M2M verifier (see newM2MVerifier).
	cfg.Auth.AllowedM2MServiceAccounts = []string{"mcp-gateway@test-project.iam.gserviceaccount.com"}
	cfg.Tasks.CallerServiceAccountEmail = "caller@test-project.iam.gserviceaccount.com"
	cfg.Tasks.AllowedServiceAccounts = []string{"web-runner@test-project.iam.gserviceaccount.com"}
	cfg.Tasks.TaskAudienceURL = "https://worker.example.test"
	cfg.Auth.GoogleClientID = "test-client-id"
	cfg.Auth.GoogleClientSecret = "test-client-secret"
	cfg.Auth.AllowedEmails = []string{"someone@example.test"}
	cfg.NormalizeModels()
	return cfg
}

// BuildHandlers leaves the handlers of faces it does not serve nil, according to SERVER_ROLE.
//
// router.go splits roles by "omitting route registration for nil handlers", so failing to leave
// one nil here would grow /tasks/generate on the web service, or have the worker hold an OAuth
// client. Neither is visible from the router's tests.
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
			// Auth and M2M are the web face's entrances. Building them on the worker would hand out access
			// to an OAuth client secret it never uses.
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

// A process serving only the web face can be assembled without a Pipeline.
// The counterpart of BuildContainer not building the generation side (Vertex AI, Veo, Slack) for role=web.
func TestBuildHandlersWebRoleNeedsNoPipeline(t *testing.T) {
	t.Parallel()

	appCtx := &app.Container{SessionStore: session.NewMemoryStore(), Config: newRoleTestConfig(serverrole.Web)}
	h, err := BuildHandlers(appCtx)
	if err != nil {
		t.Fatalf("BuildHandlers() error = %v", err)
	}
	if h.Web == nil {
		t.Fatal("web handlers were not assembled just because there is no Pipeline")
	}
}

// Startup stops when a process serving the worker face cannot configure Cloud Tasks OIDC verification.
// The verifier fails closed, so starting anyway would keep every task failing.
func TestBuildHandlersFailsWhenWorkerCannotVerifyTasks(t *testing.T) {
	t.Parallel()

	cfg := newRoleTestConfig(serverrole.Worker)
	cfg.Tasks.TaskAudienceURL = ""

	appCtx := &app.Container{SessionStore: session.NewMemoryStore(), Config: cfg, Pipeline: stubPipeline{}}
	if _, err := BuildHandlers(appCtx); err == nil {
		t.Fatal("BuildHandlers() succeeded without TASK_AUDIENCE_URL")
	}
}

// Validate rejects a shape where only one of TaskAuth and Worker is configured.
//
// router.go omits route registration by looking only at whether TaskAuth exists, so letting this
// inconsistency through would silently 404 /tasks/generate. A request cannot tell a missing
// setting from an implementation bug, so it fails at startup.
func TestAppHandlersValidateRejectsHalfConfiguredWorker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		h       *AppHandlers
		wantErr bool
	}{
		{name: "both nil (web role)", h: &AppHandlers{}},
		{
			name:    "only TaskAuth",
			h:       &AppHandlers{TaskAuth: mustOIDC(t, "https://worker.example.test", []string{"runner@example.iam.gserviceaccount.com"})},
			wantErr: true,
		},
		{
			name:    "only Worker",
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

// mustOIDC builds a configured verifier for tests (New returns an error when settings are missing).
func mustOIDC(t *testing.T, audience string, allowed []string) *oidc.Verifier {
	t.Helper()
	v, err := oidc.New(audience, allowed)
	if err != nil {
		t.Fatalf("oidc.New() error = %v", err)
	}
	return v
}
