// Package config loads and normalises the application settings from environment variables.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-serve-kit/serverrole"
	"github.com/shouni/go-utils/strlist"

	"github.com/shouni/ap-mv/internal/domain"
)

// ServerConfig holds the settings for starting and shutting down the HTTP server.
type ServerConfig struct {
	ServiceURL string `env:"SERVICE_URL" envDefault:"http://localhost:8080"`
	Port       string `env:"PORT" envDefault:"8080"`
	// Role is the role this process serves. It must be set explicitly; unset fails startup.
	Role serverrole.Role `env:"SERVER_ROLE"`
}

// GCPConfig is the GCP project information used to call Vertex AI / Cloud Tasks.
type GCPConfig struct {
	ProjectID  string `env:"GCP_PROJECT_ID"`
	LocationID string `env:"GCP_LOCATION_ID"`
}

// TasksConfig holds the settings for enqueueing to the Cloud Tasks queue.
type TasksConfig struct {
	QueueID         string `env:"CLOUD_TASKS_QUEUE_ID"`
	WorkerURL       string `env:"WORKER_URL"`
	TaskAudienceURL string `env:"TASK_AUDIENCE_URL"`
	// CallerServiceAccountEmail is the caller SA set as oidcToken.serviceAccountEmail on the
	// tasks we enqueue. Cloud Tasks generates and attaches the token; this process does not
	// sign anything. In ap-mv the worker also enqueues continuation cuts, so both roles
	// need it.
	CallerServiceAccountEmail string `env:"TASK_CALLER_SERVICE_ACCOUNT_EMAIL"`
	// AllowedServiceAccounts is the allowlist of caller SAs the worker accepts.
	// In ap-mv the worker also enqueues continuation cuts, so list both web and worker.
	AllowedServiceAccounts []string `env:"ALLOWED_TASK_SERVICE_ACCOUNTS"`
	// DispatchDeadline is the response-wait limit stamped onto the tasks we enqueue.
	//
	// It is not "how long to wait" but **the effective limit on the worker's run time**. Past it,
	// Cloud Tasks stops waiting even while the worker is still working, and with max_attempts = 1
	// on the queue no retry comes. Raising Cloud Run's timeout does not move this limit.
	// It is an env var rather than a constant so that the infrastructure side (Terraform) can be
	// its single source. As a constant, the infrastructure would hold a copy and nobody would
	// notice when they drifted.
	//
	// **There is no default.** The three-tier timeouts are decided by the deployment, so their
	// source is confined to one place in Terraform. If the app had a default, the same number
	// would appear in two places and a missing setting would run on "a value nobody chose".
	DispatchDeadline time.Duration `env:"TASK_DISPATCH_DEADLINE"`
	// PipelineTimeout is the run-time limit of one worker task. Together with DispatchDeadline
	// it forms the three-tier timeouts, so it lives here rather than among the AI generation
	// parameters (VEO_OPERATION_TIMEOUT bounds a single Veo operation, while this wraps the
	// whole step sequence: recipe generation, keyframes, video generation and publishing).
	//
	// Keep it shorter than DispatchDeadline. Equality is not enough either: unless the app can
	// give up first, Cloud Tasks cuts it off with neither a failure record nor a Slack notice
	// (on the worker, validatePipelineTimeout rejects it at startup). There is no default, for
	// the same reason as DispatchDeadline.
	PipelineTimeout time.Duration `env:"PIPELINE_TIMEOUT"`
}

// StorageConfig holds the GCS bucket settings.
type StorageConfig struct {
	GCSBucket   string `env:"AP_MV_BUCKET"`
	MusicBucket string `env:"AP_MUSIC_BUCKET"`
	// FirestoreDatabase is the Firestore database for job status.
	//
	// A named database is used so as not to take the (default) slot. It is shared by the fleet,
	// and services are separated by collection. The collection name is not configurable (it is
	// the service's identity, not a per-deployment value; see repository's statusCollection).
	FirestoreDatabase string `env:"FIRESTORE_DATABASE" envDefault:"job-status"`
}

// AIConfig holds the Gemini / Image / Veo models and generation parameters.
type AIConfig struct {
	// Model lists are comma-separated and the first is the default model. The singular
	// fields are filled by LoadConfig from the head of each list, so they are not read from
	// the environment. There are no defaults; if empty, ValidateEssentialConfig fails startup.
	GeminiModels []string `env:"GEMINI_MODELS"`
	ImageModels  []string `env:"IMAGE_MODELS"`
	VeoModels    []string `env:"VEO_MODELS"`
	GeminiModel  string   `env:"-"`
	ImageModel   string   `env:"-"`
	VeoModel     string   `env:"-"`

	VeoLocationID string `env:"VEO_LOCATION_ID"`
	// VeoOutputPrefix has no default. Where outputs go is decided by the deployment; if the
	// app had a default, a missing setting would run, writing to "a place nobody chose".
	// If empty, ValidateEssentialConfig fails startup.
	VeoOutputPrefix        string        `env:"VEO_OUTPUT_PREFIX"`
	VeoAspectRatio         string        `env:"VEO_ASPECT_RATIO" envDefault:"16:9"`
	VeoGenerateAudio       bool          `env:"VEO_GENERATE_AUDIO" envDefault:"false"`
	VeoPollInterval        time.Duration `env:"VEO_POLL_INTERVAL" envDefault:"10s"`
	VeoOperationTimeout    time.Duration `env:"VEO_OPERATION_TIMEOUT" envDefault:"20m"`
	VeoPollMaxErrors       int           `env:"VEO_POLL_MAX_ERRORS" envDefault:"10"`
	VeoUsePreviousVideo    bool          `env:"VEO_USE_PREVIOUS_VIDEO" envDefault:"false"`
	KeyframeMaxConcurrency int           `env:"KEYFRAME_MAX_CONCURRENCY" envDefault:"1"`
	KeyframeRateInterval   time.Duration `env:"KEYFRAME_RATE_INTERVAL" envDefault:"60s"`
	// KeyframeImageSize is the output resolution of keyframe images ("1K" / "2K" / "4K").
	// go-veo-orchestrator has no visual defaults, so this is the only source.
	KeyframeImageSize string `env:"KEYFRAME_IMAGE_SIZE" envDefault:"2K"`
	// VeoPriceUSDPerSec is the price table for the estimated cost on the history page (model:USD per generated second).
	// The empty key (written like ":0.40") is the fallback for models not in the table.
	//
	// There is no default. Model names and prices both change on Google's schedule, so the
	// table itself lives in the deployment config (the infrastructure repository). Unset, every
	// model is priced at domain.DefaultVeoPriceUSDPerSecond and the display does not break.
	VeoPriceUSDPerSec map[string]float64 `env:"VEO_PRICE_USD_PER_SEC" envSeparator:"," envKeyValSeparator:":"`
}

// NotificationConfig holds the settings for external notification targets.
type NotificationConfig struct {
	SlackWebhookURL string `env:"SLACK_WEBHOOK_URL"`
}

// AuthConfig holds the OAuth, session and authorisation settings.
type AuthConfig struct {
	GoogleClientID     string `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET"`
	// SessionDatabase is the Firestore database where sessions live.
	//
	// It is separate from the one for job status. A database name is an identifier that
	// cannot be changed later, so sharing one leaves the name wrong for one of the two uses.
	SessionDatabase string `env:"SESSION_FIRESTORE_DATABASE" envDefault:"sessions"`
	// SessionCollection is the collection where sessions live.
	SessionCollection string   `env:"SESSION_FIRESTORE_COLLECTION" envDefault:"sessions"`
	AllowedEmails     []string `env:"ALLOWED_EMAILS"`
	AllowedDomains    []string `env:"ALLOWED_DOMAINS"`
	// AllowedM2MServiceAccounts are the service account emails that may call the Web API
	// server-to-server (with an OIDC Bearer token). Empty disables M2M authentication.
	AllowedM2MServiceAccounts []string `env:"ALLOWED_M2M_SERVICE_ACCOUNTS"`
}

// Config is the application configuration.
type Config struct {
	Server       ServerConfig
	GCP          GCPConfig
	Tasks        TasksConfig
	Storage      StorageConfig
	AI           AIConfig
	Notification NotificationConfig
	Auth         AuthConfig
}

// normalize normalizes the provided values.
func (c *Config) normalize() error {
	c.trimStringValues()
	// Environment variable names are the app's concern, so context is added to the kit's error here.
	role, err := serverrole.Parse(string(c.Server.Role))
	if err != nil {
		return fmt.Errorf("SERVER_ROLE: %w", err)
	}
	c.Server.Role = role

	workerURL, err := normalizeWorkerURL(c.Server.Role, c.Tasks.WorkerURL, c.Server.ServiceURL)
	if err != nil {
		return err
	}
	c.Tasks.WorkerURL = workerURL
	if c.Tasks.TaskAudienceURL == "" {
		c.Tasks.TaskAudienceURL = c.Server.ServiceURL
	}
	c.Storage.GCSBucket = remoteio.NormalizeBucketName(c.Storage.GCSBucket)
	c.Storage.MusicBucket = remoteio.NormalizeBucketName(c.Storage.MusicBucket)
	c.Auth.AllowedEmails = strlist.Normalize(c.Auth.AllowedEmails)
	c.Auth.AllowedDomains = strlist.Normalize(c.Auth.AllowedDomains)
	c.Auth.AllowedM2MServiceAccounts = strlist.Normalize(c.Auth.AllowedM2MServiceAccounts)
	c.Tasks.AllowedServiceAccounts = strlist.Normalize(c.Tasks.AllowedServiceAccounts)
	// Veo is offered in limited regions (e.g. us-central1), so VEO_LOCATION_ID can override
	// GCP_LOCATION_ID, which is shared with Cloud Tasks and others. Unset uses the shared value.
	if c.AI.VeoLocationID == "" {
		c.AI.VeoLocationID = c.GCP.LocationID
	}
	c.NormalizeModels()
	return nil
}

// trimStringValues strips leading and trailing whitespace from the strings read from env.
//
// This is the one place values are made canonical. Trimming on the consumer side would let
// validation pass a value with whitespace while a different, trimmed value is what actually
// runs (a " " VEO_OUTPUT_PREFIX passes the "is not set" check and only the output location
// shifts to gs://<bucket>/jobs/, and so on).
//
// Secrets are excluded. Rewriting a key's contents would silently invalidate the sessions
// of an environment that runs with the whitespace included.
func (c *Config) trimStringValues() {
	for _, value := range []*string{
		&c.Server.ServiceURL,
		&c.Server.Port,
		&c.GCP.ProjectID,
		&c.GCP.LocationID,
		&c.Tasks.QueueID,
		&c.Tasks.TaskAudienceURL,
		&c.Tasks.CallerServiceAccountEmail,
		&c.Storage.FirestoreDatabase,
		&c.AI.VeoLocationID,
		&c.AI.VeoOutputPrefix,
		&c.AI.VeoAspectRatio,
		&c.AI.KeyframeImageSize,
		&c.Notification.SlackWebhookURL,
	} {
		*value = strings.TrimSpace(*value)
	}
}

// NormalizeModels normalizes configured model lists and defaults.
func (c *Config) NormalizeModels() {
	// The singular fields are not read from env, so there is no model to put first (preferred).
	// ModelOptions uses preferred to move the selected model to the front.
	c.AI.GeminiModels = domain.NormalizeModelList(c.AI.GeminiModels, "")
	c.AI.ImageModels = domain.NormalizeModelList(c.AI.ImageModels, "")
	c.AI.VeoModels = domain.NormalizeModelList(c.AI.VeoModels, "")
	c.AI.GeminiModel = domain.NormalizeDefaultModel(c.AI.GeminiModel, c.AI.GeminiModels)
	c.AI.ImageModel = domain.NormalizeDefaultModel(c.AI.ImageModel, c.AI.ImageModels)
	c.AI.VeoModel = domain.NormalizeDefaultModel(c.AI.VeoModel, c.AI.VeoModels)
}

// LoadConfigFromEnv loads the settings from environment variables and returns conversion errors.
func LoadConfigFromEnv() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, err
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// normalizeWorkerURL tidies the worker service URL tasks are delivered to. It returns only
// the service URL; the task path is appended just before enqueueing (internal/builder).
//
// Only processes that serve the worker face derive it from SERVICE_URL. On a split
// deployment SERVICE_URL is pinned to the public side, so deriving it on web-only would 404 every task.
func normalizeWorkerURL(role serverrole.Role, workerURL string, serviceURL string) (string, error) {
	workerURL = strings.TrimSpace(workerURL)
	if workerURL != "" {
		if _, err := url.Parse(workerURL); err != nil {
			return "", fmt.Errorf("invalid worker URL %q: %w", workerURL, err)
		}
		return workerURL, nil
	}
	if !role.ServesWorker() {
		return "", nil
	}

	serviceURL = strings.TrimSpace(serviceURL)
	if serviceURL == "" {
		return "", nil
	}
	if _, err := url.Parse(serviceURL); err != nil {
		return "", fmt.Errorf("invalid service URL %q: %w", serviceURL, err)
	}
	return serviceURL, nil
}

// GetGCSObjectURL builds the full GCS object URL ("gs://...") from the given path.
func (c *Config) GetGCSObjectURL(path string) string {
	return remoteio.BuildURI(remoteio.SchemeGCS, c.Storage.GCSBucket, path)
}

// TaskCallerServiceAccount returns the caller SA set on the tasks we enqueue.
// trimStringValues has made the value canonical.
func (c *Config) TaskCallerServiceAccount() string {
	return c.Tasks.CallerServiceAccountEmail
}
