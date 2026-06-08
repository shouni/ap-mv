package workflow

import (
	"fmt"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/keyframe"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// PromptDeps はプロンプト関連の依存関係をまとめた構造体です。
type PromptDeps struct {
	Characters     *characterkit.Characters
	ScriptPrompt   ports.ScriptPrompt
	KeyframePrompt ports.KeyframePrompt
}

// ManagerArgs は、ワークフローの初期化と管理に必要な引数の集合を表します。
type ManagerArgs struct {
	Config      ports.Config
	HTTPClient  httpkit.HTTPClient
	Reader      ports.ContentReader
	Writer      remoteio.Writer
	AIClient    gemini.GenerativeModel
	VideoRunner ports.VideoRunner
	PromptDeps  *PromptDeps
}

// generationUnit は、画像生成と構成を処理するユニットを表します
type generationUnit struct {
	imageGenerator imagePorts.ImageGenerator
	recipeComposer *keyframe.VideoComposer
	model          string
}

// manager は、ワークフローの各工程を担う Runner 群を構築・管理します。
type manager struct {
	cfg            ports.Config
	httpClient     httpkit.HTTPClient
	reader         ports.ContentReader
	writer         remoteio.Writer
	aiClient       gemini.GenerativeModel
	videoRunner    ports.VideoRunner
	generationUnit *generationUnit
	promptDeps     *PromptDeps
}

// New は、設定とキャラクター定義を基に新しい Workflows を初期化します。
func New(args ManagerArgs) (*ports.Workflows, error) {
	if err := validateArgs(&args); err != nil {
		return nil, err
	}

	cfg := args.Config
	cfg.ApplyDefaults()

	m := &manager{
		cfg:         cfg,
		httpClient:  args.HTTPClient,
		reader:      args.Reader,
		writer:      args.Writer,
		aiClient:    args.AIClient,
		videoRunner: args.VideoRunner,
		promptDeps:  args.PromptDeps,
	}

	unit, err := m.buildGenerationUnit(m.aiClient, cfg.ImageModel)
	if err != nil {
		return nil, fmt.Errorf("GenerationUnit の構築に失敗: %w", err)
	}
	m.generationUnit = unit

	return m.buildAllRunners()
}

// validateArgs は引数のバリデーションを行います。
func validateArgs(args *ManagerArgs) error {
	if args.HTTPClient == nil {
		return fmt.Errorf("HTTPClient is required")
	}
	if args.Reader == nil {
		return fmt.Errorf("InputReader is required")
	}
	if args.Writer == nil {
		return fmt.Errorf("OutputWriter is required")
	}
	if args.AIClient == nil {
		return fmt.Errorf("AIClient is required")
	}
	if args.PromptDeps == nil {
		return fmt.Errorf("PromptDeps is required")
	}
	if args.PromptDeps.Characters == nil {
		return fmt.Errorf("Characters is required")
	}
	if args.PromptDeps.ScriptPrompt == nil {
		return fmt.Errorf("ScriptPrompt is required")
	}
	if args.PromptDeps.KeyframePrompt == nil {
		return fmt.Errorf("KeyframePrompt is required")
	}

	return nil
}
