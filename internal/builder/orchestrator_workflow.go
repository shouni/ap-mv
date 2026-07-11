package builder

import (
	"context"
	"fmt"
	"path"
	"strings"

	characterassets "github.com/shouni/go-character-kit/assets"
	"github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-http-kit/httpkit"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/workflow"

	"github.com/shouni/ap-mv/assets"
	"github.com/shouni/ap-mv/internal/adapters"
	"github.com/shouni/ap-mv/internal/app"
	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/ports"
)

// buildWorkflow builds orchestrator workflows from application config.
func buildWorkflow(
	ctx context.Context,
	cfg *config.Config,
	rio *app.RemoteIO,
	httpClient httpkit.HTTPClient,
	videoRunner ports.VideoRunner,
) (*orchestrator.Workflows, error) {
	return buildWorkflowWithConfig(ctx, workflowBuildParams{
		cfg:         cfg,
		orchCfg:     buildOrchestratorConfig(cfg),
		rio:         rio,
		httpClient:  httpClient,
		videoRunner: videoRunner,
	})
}

// characterSeedOverride は、1回のワークフロー構築だけ特定キャラクターのシードを差し替える指定です。
// カット単位のキーフレーム再生成で一時的に別シードを試す用途のみに使い、
// 埋め込みキャラクター定義（go-character-kit）自体は変更しません。
type characterSeedOverride struct {
	characterID string
	seed        int64
}

// workflowBuildParams bundles buildWorkflowWithConfig's inputs into one struct. Several
// parameters share a type (two *config.Config-adjacent structs, two string-ish knobs), which
// made the previous 7-argument positional signature easy to transpose by mistake at call sites.
type workflowBuildParams struct {
	cfg          *config.Config
	orchCfg      orchestrator.Config
	rio          *app.RemoteIO
	httpClient   httpkit.HTTPClient
	videoRunner  ports.VideoRunner
	visualMode   string
	seedOverride *characterSeedOverride
}

// buildWorkflowWithConfig builds orchestrator workflows from an explicit orchestrator config.
// p.seedOverride が非nilの場合、対象キャラクターのシードのみをこの構築分に限って差し替えます。
func buildWorkflowWithConfig(ctx context.Context, p workflowBuildParams) (*orchestrator.Workflows, error) {
	if p.cfg == nil || p.rio == nil || p.rio.Reader == nil || p.rio.Writer == nil || p.httpClient == nil {
		return nil, nil
	}
	p.orchCfg.ApplyDefaults()

	aiClient, err := adapters.NewVertexAIAdapter(ctx, p.cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize gemini client: %w", err)
	}
	characters, err := buildCharacters()
	if err != nil {
		return nil, err
	}
	if p.seedOverride != nil {
		characters = withCharacterSeedOverride(characters, p.seedOverride.characterID, p.seedOverride.seed)
	}
	scriptPromptBuilder, err := newScriptPrompt()
	if err != nil {
		return nil, err
	}
	scriptPromptBuilder.visualMode = p.visualMode
	scriptPromptBuilder.characters = characters
	visualTemplates, err := assets.LoadVisualModeFiles()
	if err != nil {
		return nil, err
	}

	workflows, err := workflow.New(workflow.ManagerArgs{
		Config:      p.orchCfg,
		HTTPClient:  p.httpClient,
		Reader:      workflowReader{delegate: p.rio.Reader},
		Writer:      p.rio.Writer,
		AIClient:    aiClient,
		VideoRunner: p.videoRunner,
		PromptDeps: &workflow.PromptDeps{
			Characters:   characters,
			ScriptPrompt: scriptPromptBuilder,
			KeyframePrompt: keyframePrompt{
				styleSuffix:     p.orchCfg.StyleSuffix,
				visualMode:      p.visualMode,
				visualTemplates: visualTemplates,
			},
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

// withCharacterSeedOverride は base の該当キャラクターだけ Seed を差し替えたコピーを返します。
// 対象IDが見つからない場合は base をそのまま返します。
func withCharacterSeedOverride(base *character.Characters, characterID string, seed int64) *character.Characters {
	if base == nil || characterID == "" {
		return base
	}
	if _, ok := base.ByID[characterID]; !ok {
		return base
	}
	list := make([]character.Character, len(base.List))
	copy(list, base.List)
	byID := make(map[string]*character.Character, len(list))
	for i := range list {
		if list[i].ID == characterID {
			overridden := seed
			list[i].Seed = &overridden
		}
		byID[list[i].ID] = &list[i]
	}
	return &character.Characters{List: list, ByID: byID}
}

// workflowOutputBaseURI returns the GCS base URI for workflow outputs.
func workflowOutputBaseURI(cfg *config.Config) string {
	if cfg == nil || strings.TrimSpace(cfg.GCSBucket) == "" {
		return ""
	}
	return cfg.GetGCSObjectURL(path.Join(strings.TrimSpace(cfg.VeoOutputPrefix), "jobs"))
}
