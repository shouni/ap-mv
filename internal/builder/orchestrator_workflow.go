package builder

import (
	"context"
	"fmt"
	"path"
	"strings"

	characterassets "github.com/shouni/go-character-kit/assets"
	"github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-http-kit/httpkit"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/workflow"

	"ap-mv/assets"
	"ap-mv/internal/app"
	"ap-mv/internal/config"
	"ap-mv/internal/ports"
)

// buildWorkflow builds orchestrator workflows from application config.
func buildWorkflow(
	ctx context.Context,
	cfg *config.Config,
	rio *app.RemoteIO,
	httpClient httpkit.HTTPClient,
	videoRunner ports.VideoRunner,
) (*orchestrator.Workflows, error) {
	return buildWorkflowWithConfig(ctx, cfg, buildOrchestratorConfig(cfg), rio, httpClient, videoRunner, "")
}

// buildWorkflowWithConfig builds orchestrator workflows from an explicit orchestrator config.
func buildWorkflowWithConfig(
	ctx context.Context,
	cfg *config.Config,
	orchCfg orchestrator.Config,
	rio *app.RemoteIO,
	httpClient httpkit.HTTPClient,
	videoRunner ports.VideoRunner,
	visualMode string,
) (*orchestrator.Workflows, error) {
	if cfg == nil || rio == nil || rio.Reader == nil || rio.Writer == nil || httpClient == nil {
		return nil, nil
	}
	orchCfg.ApplyDefaults()

	aiClient, err := gemini.NewClient(ctx, geminiConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize gemini client: %w", err)
	}
	characters, err := buildCharacters()
	if err != nil {
		return nil, err
	}
	scriptPrompt, err := newScriptPrompt()
	if err != nil {
		return nil, err
	}
	scriptPrompt.visualMode = visualMode
	visualTemplates, err := assets.LoadVisualModeFiles()
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
			Characters:   characters,
			ScriptPrompt: scriptPrompt,
			KeyframePrompt: keyframePrompt{
				styleSuffix:     orchCfg.StyleSuffix,
				visualMode:      visualMode,
				visualTemplates: visualTemplates,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize workflows: %w", err)
	}
	return workflows, nil
}

// geminiConfig returns the Gemini client configuration.
func geminiConfig(cfg *config.Config) gemini.Config {
	if apiKey := strings.TrimSpace(cfg.GeminiAPIKey); apiKey != "" {
		return gemini.Config{APIKey: apiKey}
	}
	return gemini.Config{
		ProjectID:  strings.TrimSpace(cfg.ProjectID),
		LocationID: strings.TrimSpace(cfg.LocationID),
	}
}

// buildCharacters builds characters.
func buildCharacters() (*character.Characters, error) {
	chars, err := characterassets.LoadCharacters()
	if err != nil {
		return nil, fmt.Errorf("load bundled characters: %w", err)
	}
	return chars, nil
}

// workflowOutputBaseURI returns the GCS base URI for workflow outputs.
func workflowOutputBaseURI(cfg *config.Config) string {
	if cfg == nil || strings.TrimSpace(cfg.GCSBucket) == "" {
		return ""
	}
	return cfg.GetGCSObjectURL(path.Join(strings.TrimSpace(cfg.VeoOutputPrefix), "jobs"))
}
