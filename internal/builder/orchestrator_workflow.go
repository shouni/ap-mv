package builder

import (
	"context"
	"fmt"
	"path"

	"github.com/shouni/genai-kit/gemini"
	characterassets "github.com/shouni/go-character-kit/assets"
	"github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/workflow"

	"github.com/shouni/ap-mv/internal/adapters/prompt"
	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/ports"
)

// buildWorkflow builds orchestrator workflows from application config.
func buildWorkflow(
	ctx context.Context,
	cfg *config.Config,
	store remoteio.Store,
	httpClient httpkit.HTTPClient,
	videoRunner ports.VideoRunner,
	aiClient gemini.Generator,
) (*orchestrator.Workflows, error) {
	return buildWorkflowWithConfig(ctx, workflowBuildParams{
		cfg:         cfg,
		orchCfg:     buildOrchestratorConfig(cfg),
		store:       store,
		httpClient:  httpClient,
		videoRunner: videoRunner,
		aiClient:    aiClient,
	})
}

// characterSeedOverride replaces a specific character's seed for a single workflow build only.
// It is used only to try a different seed temporarily in per-cut keyframe regeneration, and the
// embedded character definitions (go-character-kit) themselves are not changed.
type characterSeedOverride struct {
	characterID string
	seed        int64
}

// workflowBuildParams bundles buildWorkflowWithConfig's inputs into one struct. Several
// parameters share a type (two *config.Config-adjacent structs, two string-ish knobs), which
// made the previous 7-argument positional signature easy to transpose by mistake at call sites.
type workflowBuildParams struct {
	cfg         *config.Config
	orchCfg     orchestrator.Config
	store       remoteio.Store
	httpClient  httpkit.HTTPClient
	videoRunner ports.VideoRunner
	// aiClient is the Vertex AI client BuildContainer built. Workflows are rebuilt per task (seed
	// overrides, visual modes), so creating it here every time would mean one client per task, down
	// to resolving ADC's token source all over again.
	aiClient     gemini.Generator
	visualMode   string
	seedOverride *characterSeedOverride
}

// buildWorkflowWithConfig builds orchestrator workflows from an explicit orchestrator config.
// When p.seedOverride is non-nil, only the target character's seed is replaced, for this build only.
// ctx is currently unused: creating the Vertex AI client moved to the caller, and nothing left in
// this function does I/O. The parameter is kept (so the caller chain need not be rebuilt if I/O
// returns to building workflows).
func buildWorkflowWithConfig(_ context.Context, p workflowBuildParams) (*orchestrator.Workflows, error) {
	if p.cfg == nil || p.store == nil || p.httpClient == nil {
		return nil, nil
	}
	if p.aiClient == nil {
		return nil, fmt.Errorf("aiClient is required")
	}
	p.orchCfg.ApplyDefaults()

	characters, err := buildCharacters()
	if err != nil {
		return nil, err
	}
	if p.seedOverride != nil {
		characters = characters.WithSeedOverride(p.seedOverride.characterID, p.seedOverride.seed)
	}
	scriptPrompt, err := prompt.NewScript(p.visualMode, characters)
	if err != nil {
		return nil, err
	}
	keyframePrompt, err := prompt.NewKeyframe(prompt.DefaultStyleSuffix, p.visualMode)
	if err != nil {
		return nil, err
	}

	workflows, err := workflow.New(workflow.ManagerArgs{
		Config:      p.orchCfg,
		Reader:      workflowReader{delegate: p.store},
		Writer:      p.store,
		AIClient:    p.aiClient,
		VideoRunner: p.videoRunner,
		PromptDeps: &workflow.PromptDeps{
			Characters:     characters,
			ScriptPrompt:   scriptPrompt,
			KeyframePrompt: keyframePrompt,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize workflows: %w", err)
	}
	return workflows, nil
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
	if cfg == nil || cfg.Storage.GCSBucket == "" {
		return ""
	}
	return cfg.GetGCSObjectURL(path.Join(cfg.AI.VeoOutputPrefix, "jobs"))
}
