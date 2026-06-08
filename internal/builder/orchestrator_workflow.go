package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-http-kit/httpkit"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/workflow"

	"ap-mv/internal/app"
	"ap-mv/internal/config"
	"ap-mv/internal/ports"
)

func buildWorkflow(
	ctx context.Context,
	cfg *config.Config,
	rio *app.RemoteIO,
	httpClient httpkit.HTTPClient,
	videoRunner ports.VideoRunner,
) (*orchestrator.Workflows, error) {
	return buildWorkflowWithConfig(ctx, cfg, buildOrchestratorConfig(cfg), rio, httpClient, videoRunner)
}

func buildWorkflowWithConfig(
	ctx context.Context,
	cfg *config.Config,
	orchCfg orchestrator.Config,
	rio *app.RemoteIO,
	httpClient httpkit.HTTPClient,
	videoRunner ports.VideoRunner,
) (*orchestrator.Workflows, error) {
	if cfg == nil || rio == nil || rio.Reader == nil || rio.Writer == nil || httpClient == nil {
		return nil, nil
	}
	orchCfg.ApplyDefaults()

	aiClient, err := gemini.NewClient(ctx, geminiConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize gemini client: %w", err)
	}
	characters, err := buildCharacters(cfg)
	if err != nil {
		return nil, err
	}
	scriptPrompt, err := newScriptPrompt()
	if err != nil {
		return nil, err
	}

	workflows, err := workflow.New(workflow.ManagerArgs{
		Config:      orchCfg,
		HTTPClient:  httpClient,
		Reader:      workflowReader{delegate: rio.Reader},
		Writer:      rio.Writer,
		AIClient:    aiClient,
		VideoRunner: videoRunner,
		PromptDeps: &workflow.PromptDeps{
			Characters:     characters,
			ScriptPrompt:   scriptPrompt,
			KeyframePrompt: keyframePrompt{styleSuffix: orchCfg.StyleSuffix},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize workflows: %w", err)
	}
	return workflows, nil
}

func geminiConfig(cfg *config.Config) gemini.Config {
	if apiKey := strings.TrimSpace(cfg.GeminiAPIKey); apiKey != "" {
		return gemini.Config{APIKey: apiKey}
	}
	return gemini.Config{
		ProjectID:  strings.TrimSpace(cfg.ProjectID),
		LocationID: strings.TrimSpace(cfg.LocationID),
	}
}

func buildCharacters(cfg *config.Config) (*characterkit.Characters, error) {
	referenceURL := defaultCharacterReferenceURL(cfg)
	data, err := json.Marshal([]characterkit.Character{
		{
			ID:           "default",
			Name:         "Main character",
			ReferenceURL: referenceURL,
			VisualCues: []string{
				"consistent protagonist design",
				"expressive anime-style face",
				"clear silhouette",
			},
			Seed:      int64Ptr(10001),
			IsDefault: true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal default characters: %w", err)
	}
	return characterkit.ParseCharacters(data)
}

func int64Ptr(v int64) *int64 {
	return &v
}

func defaultCharacterReferenceURL(cfg *config.Config) string {
	if cfg != nil {
		if ref := strings.TrimSpace(cfg.CharacterReferenceURL); ref != "" {
			return ref
		}
	}
	if cfg == nil || strings.TrimSpace(cfg.GCSBucket) == "" {
		return "gs://example-bucket/ap-mv/characters/default.png"
	}
	return cfg.GetGCSObjectURL(path.Join("ap-mv", "characters", "default.png"))
}

func workflowOutputBaseURI(cfg *config.Config) string {
	if cfg == nil || strings.TrimSpace(cfg.GCSBucket) == "" {
		return ""
	}
	return cfg.GetGCSObjectURL(path.Join(strings.TrimSpace(cfg.VeoOutputPrefix), "jobs"))
}
