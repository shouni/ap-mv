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

	"github.com/shouni/ap-mv/internal/adapters/prompt"
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
	aiClient gemini.Model,
) (*orchestrator.Workflows, error) {
	return buildWorkflowWithConfig(ctx, workflowBuildParams{
		cfg:         cfg,
		orchCfg:     buildOrchestratorConfig(cfg),
		rio:         rio,
		httpClient:  httpClient,
		videoRunner: videoRunner,
		aiClient:    aiClient,
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
	cfg         *config.Config
	orchCfg     orchestrator.Config
	rio         *app.RemoteIO
	httpClient  httpkit.HTTPClient
	videoRunner ports.VideoRunner
	// aiClient は BuildContainer が組んだ Vertex AI クライアントです。ワークフローは
	// タスクごとに組み直される（シード上書き・ビジュアルモード）ため、ここで都度
	// 生成すると 1 タスクにつき 1 クライアント、つまり ADC のトークンソース解決まで
	// やり直すことになります。
	aiClient     gemini.Model
	visualMode   string
	seedOverride *characterSeedOverride
}

// buildWorkflowWithConfig builds orchestrator workflows from an explicit orchestrator config.
// p.seedOverride が非nilの場合、対象キャラクターのシードのみをこの構築分に限って差し替えます。
// ctx は現在使っていません。Vertex AI クライアントの生成が呼び出し側へ移り、この関数に
// 残った処理がいずれも I/O を伴わなくなったためです。引数は残してあります（ワークフロー
// 構築に I/O が戻ったときに、呼び出し側の連鎖を作り直さずに済むように）。
func buildWorkflowWithConfig(_ context.Context, p workflowBuildParams) (*orchestrator.Workflows, error) {
	if p.cfg == nil || p.rio == nil || p.rio.Reader == nil || p.rio.Writer == nil || p.httpClient == nil {
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
		Reader:      workflowReader{delegate: p.rio.Reader},
		Writer:      p.rio.Writer,
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
	if cfg == nil || strings.TrimSpace(cfg.Storage.GCSBucket) == "" {
		return ""
	}
	return cfg.GetGCSObjectURL(path.Join(strings.TrimSpace(cfg.AI.VeoOutputPrefix), "jobs"))
}
