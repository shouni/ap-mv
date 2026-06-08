package workflow

import (
	"github.com/shouni/go-veo-orchestrator/keyframe"
	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/runner"
)

// buildAllRunners は、ワークフローの実行に必要なすべてのランナーを構築して返します。
func (m *manager) buildAllRunners() (*ports.Workflows, error) {
	sr, err := m.buildScriptRunner()
	if err != nil {
		return nil, err
	}
	keyframeR, err := m.buildKeyframeRunner()
	if err != nil {
		return nil, err
	}
	pubR, err := m.buildPublishRunner()
	if err != nil {
		return nil, err
	}
	videoR := m.buildVideoTimelineRunner(keyframeR, pubR)

	return &ports.Workflows{
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

// buildKeyframeRunner は、カットのキーフレーム画像生成を担当する Runner を作成します。
func (m *manager) buildKeyframeRunner() (*runner.CutKeyframeRunner, error) {
	keyframeGen := keyframe.NewKeyframeGenerator(
		m.generationUnit.recipeComposer,
		m.generationUnit.imageGenerator,
		m.promptDeps.KeyframePrompt,
		m.generationUnit.model,
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
