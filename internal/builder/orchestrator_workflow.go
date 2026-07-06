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
	return buildWorkflowWithConfig(ctx, cfg, buildOrchestratorConfig(cfg), rio, httpClient, videoRunner, "", nil)
}

// characterSeedOverride は、1回のワークフロー構築だけ特定キャラクターのシードを差し替える指定です。
// カット単位のキーフレーム再生成で一時的に別シードを試す用途のみに使い、
// 埋め込みキャラクター定義（go-character-kit）自体は変更しません。
type characterSeedOverride struct {
	characterID string
	seed        int64
}

// buildWorkflowWithConfig builds orchestrator workflows from an explicit orchestrator config.
// seedOverride が非nilの場合、対象キャラクターのシードのみをこの構築分に限って差し替えます。
func buildWorkflowWithConfig(
	ctx context.Context,
	cfg *config.Config,
	orchCfg orchestrator.Config,
	rio *app.RemoteIO,
	httpClient httpkit.HTTPClient,
	videoRunner ports.VideoRunner,
	visualMode string,
	seedOverride *characterSeedOverride,
) (*orchestrator.Workflows, error) {
	if cfg == nil || rio == nil || rio.Reader == nil || rio.Writer == nil || httpClient == nil {
		return nil, nil
	}
	orchCfg.ApplyDefaults()

	aiClient, err := adapters.NewVertexAIAdapter(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize gemini client: %w", err)
	}
	characters, err := buildCharacters()
	if err != nil {
		return nil, err
	}
	if seedOverride != nil {
		characters = withCharacterSeedOverride(characters, seedOverride.characterID, seedOverride.seed)
	}
	scriptPromptBuilder, err := newScriptPrompt()
	if err != nil {
		return nil, err
	}
	scriptPromptBuilder.visualMode = visualMode
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
			ScriptPrompt: scriptPromptBuilder,
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
