package workflow

import (
	"fmt"

	"github.com/shouni/go-veo-orchestrator/keyframe"
	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/runner"
)

// buildAllRunners は、ワークフローの実行に必要なすべてのランナーを構築して返します。
func (m *manager) buildAllRunners() (*ports.Workflows, error) {
	dr, err := m.buildDesignRunner()
	if err != nil {
		return nil, fmt.Errorf("DesignRunner のビルドに失敗しました: %w", err)
	}
	sr, err := m.buildScriptRunner()
	if err != nil {
		return nil, fmt.Errorf("ScriptRunner のビルドに失敗しました: %w", err)
	}
	keyframeR, err := m.buildKeyframeRunner()
	if err != nil {
		return nil, fmt.Errorf("KeyframeRunner のビルドに失敗しました: %w", err)
	}
	pubR, err := m.buildPublishRunner()
	if err != nil {
		return nil, fmt.Errorf("PublishRunner のビルドに失敗しました: %w", err)
	}
	videoR := m.buildVideoTimelineRunner(keyframeR, pubR)

	return &ports.Workflows{
		Design:      dr,
		Script:      sr,
		CutKeyframe: keyframeR,
		Video:       videoR,
		Publish:     pubR,
	}, nil
}

// buildScriptRunner は、台本生成を担当する Runner を作成します。
func (m *manager) buildScriptRunner() (*runner.VideoScriptRunner, error) {
	return runner.NewVideoScriptRunner(m.promptDeps.ScriptPrompt, m.aiClient, m.reader, m.cfg.GeminiModel), nil
}

// buildDesignRunner は、キャラクターデザインを担当する Runner を作成します。
func (m *manager) buildDesignRunner() (*runner.DesignRunner, error) {
	quality := m.generationManager.Quality
	return runner.NewDesignRunner(
		quality.recipeComposer,
		quality.imageGenerator,
		m.writer,
		quality.model,
		m.cfg.StyleSuffix,
	), nil
}

// buildKeyframeRunner は、カットのキーフレーム画像生成を担当する Runner を作成します。
func (m *manager) buildKeyframeRunner() (*runner.CutKeyframeRunner, error) {
	standard := m.generationManager.Standard
	keyframeGen := keyframe.NewKeyframeGenerator(
		standard.recipeComposer,
		standard.imageGenerator,
		m.promptDeps.KeyframePrompt,
		standard.model,
		keyframe.WithKeyframeMaxConcurrency(m.cfg.MaxConcurrency),
		keyframe.WithKeyframeRateInterval(m.cfg.RateInterval),
	)

	return runner.NewCutKeyframeRunner(keyframeGen, m.writer), nil
}

// buildPublishRunner は、動画メタデータのパブリッシュを担当する Runner を作成します。
func (m *manager) buildPublishRunner() (*runner.VideoPublisherRunner, error) {
	return runner.NewVideoPublisherRunner(m.writer), nil
}

// buildVideoTimelineRunner は、キーフレーム生成から Veo 生成までをつなぐ Runner を作成します。
func (m *manager) buildVideoTimelineRunner(
	keyframeRunner ports.CutKeyframeRunner,
	publisher ports.VideoPublishRunner,
) ports.VideoTimelineRunner {
	if m.videoRunner == nil {
		return nil
	}
	return runner.NewVideoTimelineRunner(keyframeRunner, m.videoRunner, publisher)
}
