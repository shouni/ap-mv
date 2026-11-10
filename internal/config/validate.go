package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/shouni/netarmor/securenet"

	"github.com/shouni/ap-mv/internal/domain"
)

// IsSecureServiceURL checks whether the configured ServiceURL uses a secure scheme (such as HTTPS).
func (c *Config) IsSecureServiceURL() bool {
	return securenet.IsSecureServiceURL(c.Server.ServiceURL)
}

// IsSecureWorkerURL checks whether the Cloud Tasks delivery URL uses a secure scheme.
func (c *Config) IsSecureWorkerURL() bool {
	return securenet.IsSecureServiceURL(c.Tasks.WorkerURL)
}

// ValidateEssentialConfig validates the settings essential for running the application.
func (c *Config) ValidateEssentialConfig() error {
	if !c.IsSecureServiceURL() {
		return fmt.Errorf("SERVICE_URL ('%s') must be HTTPS in production", c.Server.ServiceURL)
	}
	if c.GCP.ProjectID == "" {
		return fmt.Errorf("GCP_PROJECT_ID is not set (required for running on Vertex AI)")
	}
	if c.GCP.LocationID == "" {
		return fmt.Errorf("GCP_LOCATION_ID is not set (default: asia-northeast1)")
	}
	if c.TaskCallerServiceAccount() == "" {
		return fmt.Errorf("TASK_CALLER_SERVICE_ACCOUNT_EMAIL is not set")
	}
	// Unlike the music-generation service, ap-mv's worker also enqueues tasks: it generates
	// video split into cuts and, when cuts remain, re-enqueues the next one itself
	// (internal/pipeline/step/video_gen.go). So the queue and WORKER_URL are required for
	// both roles.
	if c.Tasks.QueueID == "" {
		return fmt.Errorf("CLOUD_TASKS_QUEUE_ID is not set")
	}
	// Only processes serving the worker face derive an unset value from SERVICE_URL, so in a
	// web-only process this stays empty. Letting it through would enqueue tasks with no destination.
	if c.Tasks.WorkerURL == "" {
		return fmt.Errorf("WORKER_URL is not set (pass the worker service's URL)")
	}
	if !c.IsSecureWorkerURL() {
		return fmt.Errorf("WORKER_URL ('%s') must be HTTPS in production", c.Tasks.WorkerURL)
	}
	// normalize() has already normalised the values (validation does not rewrite the config).
	if c.Storage.GCSBucket == "" {
		return fmt.Errorf("AP_MV_BUCKET is not set")
	}
	// Model lists are required regardless of role: the worker uses them to generate and web for
	// the submission form's options, so validating only one would let the other start up empty.
	for _, m := range []struct {
		envKey string
		models []string
	}{
		{"GEMINI_MODELS", c.AI.GeminiModels},
		{"IMAGE_MODELS", c.AI.ImageModels},
		{"VEO_MODELS", c.AI.VeoModels},
	} {
		if len(m.models) == 0 {
			return fmt.Errorf("%s is not set (give several comma-separated; the first is the default and they become the form's options)", m.envKey)
		}
	}
	if c.AI.VeoOutputPrefix == "" {
		return fmt.Errorf("VEO_OUTPUT_PREFIX is not set")
	}
	if !domain.IsAllowedAspectRatio(c.AI.VeoAspectRatio) {
		return fmt.Errorf("VEO_ASPECT_RATIO must be one of %s", strings.Join(domain.AllowedAspectRatios, " / "))
	}
	if c.AI.VeoPollInterval <= 0 {
		return fmt.Errorf("VEO_POLL_INTERVAL must be a positive duration")
	}
	if c.AI.VeoOperationTimeout <= 0 {
		return fmt.Errorf("VEO_OPERATION_TIMEOUT must be a positive duration")
	}
	// go-veo-orchestrator has no visual defaults, so this is the only source.
	// Silently passing an invalid value would render at a resolution the model chose, unnoticed.
	if !domain.IsAllowedImageSize(c.AI.KeyframeImageSize) {
		return fmt.Errorf("KEYFRAME_IMAGE_SIZE must be one of %s", strings.Join(domain.AllowedImageSizes, " / "))
	}
	c.warnContradictoryKeyframeThroughput()

	if c.Server.Role.ServesWeb() {
		if err := c.validateWebConfig(); err != nil {
			return err
		}
	}

	if c.Server.Role.ServesWorker() {
		if c.Tasks.TaskAudienceURL == "" {
			return fmt.Errorf("TASK_AUDIENCE_URL is not set; it is required for Cloud Tasks OIDC verification")
		}
		// Empty makes the verifier fail closed, and every task keeps failing with 500.
		if len(c.Tasks.AllowedServiceAccounts) == 0 {
			return fmt.Errorf("no caller SA is allowed; set ALLOWED_TASK_SERVICE_ACCOUNTS")
		}
		if err := c.validatePipelineTimeout(); err != nil {
			return err
		}
	}

	return nil
}

// validatePipelineTimeout checks, for roles that run the pipeline, that PIPELINE_TIMEOUT is
// strictly shorter than TASK_DISPATCH_DEADLINE.
//
// The point is that the app gives up first. In the reverse order (equality included) Cloud
// Tasks cuts the request first, neither the failure record nor the Slack notice runs, and the
// queue does not retry with max_attempts = 1, so the task is left at running. Unlimited (0 or
// less) is rejected for the same reason. The effective cut-off is the smallest of the three,
// so this one relation is enforced at startup.
func (c *Config) validatePipelineTimeout() error {
	if c.Tasks.DispatchDeadline <= 0 {
		return fmt.Errorf("TASK_DISPATCH_DEADLINE is not set (the deployment config decides the three-tier timeouts, e.g. 30m)")
	}
	if c.Tasks.PipelineTimeout <= 0 {
		return fmt.Errorf("PIPELINE_TIMEOUT cannot be unlimited on the worker; set it shorter than the Cloud Tasks cut-off (%s)", c.Tasks.DispatchDeadline)
	}
	if c.Tasks.PipelineTimeout >= c.Tasks.DispatchDeadline {
		return fmt.Errorf("PIPELINE_TIMEOUT (%s) must be shorter than the Cloud Tasks cut-off (%s); even when equal, the request is cut before the app records the failure", c.Tasks.PipelineTimeout, c.Tasks.DispatchDeadline)
	}
	return nil
}

// warnContradictoryKeyframeThroughput warns about a setting that raises both concurrency and the interval.
//
// go-veo-orchestrator spaces AI calls with one limiter, so throughput caps at
// 1/KEYFRAME_RATE_INTERVAL regardless of concurrency. Raising only the concurrency does not
// speed anything up, and since it is not an error, whoever set it keeps waiting believing it
// works. Logging the effective rate at startup makes that mistake visible.
func (c *Config) warnContradictoryKeyframeThroughput() {
	if c.AI.KeyframeMaxConcurrency <= 1 || c.AI.KeyframeRateInterval <= 0 {
		return
	}
	slog.Warn("KEYFRAME_MAX_CONCURRENCY is capped by KEYFRAME_RATE_INTERVAL",
		"keyframe_max_concurrency", c.AI.KeyframeMaxConcurrency,
		"keyframe_rate_interval", c.AI.KeyframeRateInterval,
		"effective_images_per_minute", int(time.Minute/c.AI.KeyframeRateInterval))
}

// validateWebConfig validates the settings the web face (OAuth login and sessions) needs.
// Requiring OAuth settings from a process that serves only the worker face would hand out
// access to credentials it never uses, so the check is split by role.
func (c *Config) validateWebConfig() error {
	if c.Auth.GoogleClientID == "" || c.Auth.GoogleClientSecret == "" {
		return fmt.Errorf("google OAuth settings (ClientID, ClientSecret) are missing")
	}

	if len(c.Auth.AllowedEmails) == 0 && len(c.Auth.AllowedDomains) == 0 {
		return fmt.Errorf("no allowed email address or domain is set (the authorisation list is empty)")
	}

	return nil
}
