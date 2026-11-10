package config

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shouni/go-serve-kit/serverrole"
)

// newRoleTestConfig returns the base config for tests that only look at per-role validation.
// Web-face-specific settings (OAuth, authorisation lists) are deliberately left empty,
// and each test fills them in as needed.
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
	// Model lists are required regardless of role (web for the options, worker to generate).
	cfg.AI.GeminiModels = []string{"gemini-test-flash"}
	cfg.AI.ImageModels = []string{"gemini-test-image"}
	cfg.AI.VeoModels = []string{"veo-test"}
	cfg.AI.VeoModel = "veo-test"
	cfg.AI.VeoOutputPrefix = "veo"
	cfg.AI.VeoAspectRatio = "16:9"
	// go-veo-orchestrator has no defaults for visual values, so the app requires them.
	cfg.AI.KeyframeImageSize = "2K"
	cfg.AI.VeoPollInterval = 5 * time.Second
	cfg.AI.VeoOperationTimeout = 10 * time.Minute
	// env defaults do not go into a bare struct, so match the state of a real config.
	// Left at 0, the worker's validation fails on "unlimited".
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

// TestValidateEssentialConfigSkipsWebRequirementsForWorker checks that a worker-only process
// can start without OAuth settings. Without this, the worker's service account would have to
// be granted access to credentials it never uses.
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

// TestValidateEssentialConfigRequiresQueueForBothRoles checks that the worker needs the queue
// too. Unlike the music-generation service, ap-mv's worker generates video split into cuts and,
// when cuts remain, re-enqueues the next one itself, so it is also an enqueuer
// (internal/pipeline/step/video_gen.go).
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

// TestValidateEssentialConfigRequiresTaskAudienceForWorker checks that the worker does not
// start without an audience. Unset, the OIDC verifier fails closed and every task keeps
// failing with 500.
func TestValidateEssentialConfigRequiresTaskAudienceForWorker(t *testing.T) {
	cfg := newRoleTestConfig(serverrole.Worker)
	cfg.Tasks.TaskAudienceURL = ""

	if err := cfg.ValidateEssentialConfig(); err == nil {
		t.Fatal("expected an error when TASK_AUDIENCE_URL is missing")
	}
}

// TestValidateEssentialConfigRequiresAllowlistForWorker checks that the worker does not start
// with no allowed issuer at all. Empty, the verifier fails closed and every task keeps
// failing with 500.
func TestValidateEssentialConfigRequiresAllowlistForWorker(t *testing.T) {
	cfg := newRoleTestConfig(serverrole.Worker)
	cfg.Tasks.AllowedServiceAccounts = nil

	if err := cfg.ValidateEssentialConfig(); err == nil {
		t.Fatal("expected an error when no task issuer is configured")
	}
}

// TestNormalizeTrimsConfiguredValues checks that normalize() strips leading and trailing
// whitespace from the values read from env.
//
// Values are made canonical in one place because trimming on the consumer side makes
// validation and actual behaviour disagree. A whitespace-only VEO_OUTPUT_PREFIX slipped past
// the "is not set" check while only the output location shifted to gs://<bucket>/jobs/.
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

// A whitespace-only VEO_OUTPUT_PREFIX fails startup. normalize() turns it into empty, so
// validation itself is the gate.
func TestValidateEssentialConfigRejectsBlankVeoOutputPrefix(t *testing.T) {
	cfg := withWebAuth(newRoleTestConfig(serverrole.Both))
	cfg.AI.VeoOutputPrefix = "   "

	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize() error = %v", err)
	}

	err := cfg.ValidateEssentialConfig()
	if err == nil {
		t.Fatal("a whitespace-only VEO_OUTPUT_PREFIX passed startup validation")
	}
	if !strings.Contains(err.Error(), "VEO_OUTPUT_PREFIX") {
		t.Fatalf("error = %v, want it to mention VEO_OUTPUT_PREFIX", err)
	}
}

// TestValidateEssentialConfigRequiresKeyframeImageSize checks that an invalid resolution fails
// startup. go-veo-orchestrator has no visual defaults, so silently passing it would render at
// a resolution the model chose, unnoticed.
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

// TestWarnContradictoryKeyframeThroughput checks the condition for detecting a setting that
// raises both concurrency and the interval. Throughput caps at the interval regardless of
// concurrency, so this combination "looks like it works but does not" (it is not an error:
// it is a valid setting operationally, and may be a deliberate throttle).
func TestWarnContradictoryKeyframeThroughput(t *testing.T) {
	tests := map[string]struct {
		concurrency int
		interval    time.Duration
		wantWarn    bool
	}{
		"concurrency 1 is not contradictory": {concurrency: 1, interval: 60 * time.Second, wantWarn: false},
		"no interval is not contradictory":   {concurrency: 4, interval: 0, wantWarn: false},
		"both high caps throughput":          {concurrency: 4, interval: 60 * time.Second, wantWarn: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := newRoleTestConfig(serverrole.Worker)
			cfg.AI.KeyframeMaxConcurrency = tt.concurrency
			cfg.AI.KeyframeRateInterval = tt.interval

			// The warning itself only goes to slog, so check the condition directly.
			got := cfg.AI.KeyframeMaxConcurrency > 1 && cfg.AI.KeyframeRateInterval > 0
			if got != tt.wantWarn {
				t.Errorf("warn = %v, want %v", got, tt.wantWarn)
			}
			cfg.warnContradictoryKeyframeThroughput() // must not panic
		})
	}
}

// TestValidateEssentialConfigRequiresPipelineTimeoutUnderDispatchDeadline checks that the
// worker starts only with a setting where it "gives up before Cloud Tasks does".
//
// Equal, longer and unlimited are all the same in that the cut-off comes from Cloud Tasks.
// Then the whole process is stopped, so neither the failure handler nor the Slack notice runs,
// and with max_attempts = 1 there is no retry, leaving the task at running. It also pins that
// the defaults satisfy this relation.
func TestValidateEssentialConfigRequiresPipelineTimeoutUnderDispatchDeadline(t *testing.T) {
	// The default exists only in the envDefault tag, so read the tag itself.
	// If it were at or above the cut-off, no worker without PIPELINE_TIMEOUT could start.
	// There is no default. The source is one place: the deployment config (Terraform).
	t.Run("has no default", func(t *testing.T) {
		field, ok := reflect.TypeFor[TasksConfig]().FieldByName("PipelineTimeout")
		if !ok {
			t.Fatal("TasksConfig.PipelineTimeout not found")
		}
		if got := field.Tag.Get("envDefault"); got != "" {
			t.Errorf("envDefault = %q, do not give it a default", got)
		}
		if err := newRoleTestConfig(serverrole.Worker).ValidateEssentialConfig(); err != nil {
			t.Fatalf("worker should start with a valid timeout: %v", err)
		}
	})

	for _, tt := range []struct {
		name    string
		timeout time.Duration
	}{
		{name: "equal to the cut-off fails", timeout: testDispatchDeadline},
		{name: "longer than the cut-off fails", timeout: testDispatchDeadline + time.Minute},
		{name: "unlimited fails", timeout: 0},
		{name: "negative unlimited fails too", timeout: -1},
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

	// web does not build the pipeline, so it is outside this check.
	t.Run("web can start with unlimited", func(t *testing.T) {
		cfg := withWebAuth(newRoleTestConfig(serverrole.Web))
		cfg.Tasks.PipelineTimeout = 0

		if err := cfg.ValidateEssentialConfig(); err != nil {
			t.Fatalf("web should not be subject to the check: %v", err)
		}
	})
}

// The cut-off has no default.
//
// The three-tier timeouts are decided by the deployment, so their source is confined to one
// place in Terraform. If the app had a default, the same number would appear in two places and
// a missing setting would run on "a value nobody chose".
func TestDispatchDeadlineHasNoDefault(t *testing.T) {
	field, ok := reflect.TypeFor[TasksConfig]().FieldByName("DispatchDeadline")
	if !ok {
		t.Fatal("TasksConfig.DispatchDeadline not found")
	}
	if got := field.Tag.Get("envDefault"); got != "" {
		t.Errorf("envDefault = %q, do not give it a default", got)
	}
}

// Unset fails startup.
func TestDispatchDeadlineIsRequired(t *testing.T) {
	cfg := newRoleTestConfig(serverrole.Worker)
	cfg.Tasks.DispatchDeadline = 0

	err := cfg.ValidateEssentialConfig()
	if err == nil {
		t.Fatal("an unset value slipped through")
	}
	if !strings.Contains(err.Error(), "TASK_DISPATCH_DEADLINE") {
		t.Errorf("the error does not name the variable: %v", err)
	}
}

// The cut-off can be raised through env. While it was a constant, that needed a rebuild.
func TestDispatchDeadlineIsConfigurable(t *testing.T) {
	cfg := newRoleTestConfig(serverrole.Worker)
	cfg.Tasks.DispatchDeadline = 20 * time.Minute
	cfg.Tasks.PipelineTimeout = 25 * time.Minute

	if err := cfg.ValidateEssentialConfig(); err == nil {
		t.Fatal("a PIPELINE_TIMEOUT longer than the shortened cut-off was accepted")
	}

	cfg.Tasks.DispatchDeadline = 30 * time.Minute
	if err := cfg.ValidateEssentialConfig(); err != nil {
		t.Fatalf("the raised combination failed: %v", err)
	}
}

// testDispatchDeadline is the cut-off used in tests. The app has no default, so set it
// explicitly, as the real deployment config does.
const testDispatchDeadline = 30 * time.Minute
