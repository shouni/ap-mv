package builder

import (
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/adapters/prompt"
	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
)

// buildOrchestratorConfig builds orchestrator config.
//
// アスペクト比・解像度・ネガティブプロンプトは go-veo-orchestrator が既定値を持たないため、
// このアプリが唯一の出所です（キットが既定を併せ持つと VEO_ASPECT_RATIO と二重の出所になり、
// 片方だけ変えたときにキーフレームだけ別の比率で焼かれます）。
func buildOrchestratorConfig(cfg *config.Config) orchestrator.Config {
	orchCfg := orchestrator.Config{
		KeyframeAspectRatio:    domain.DefaultAspectRatio,
		KeyframeImageSize:      "2K",
		KeyframeNegativePrompt: prompt.DefaultKeyframeNegativePrompt,
	}
	if cfg == nil {
		orchCfg.ApplyDefaults()
		return orchCfg
	}

	orchCfg.GeminiModel = cfg.AI.GeminiModel
	orchCfg.ImageModel = cfg.AI.ImageModel
	orchCfg.MaxConcurrency = cfg.AI.KeyframeMaxConcurrency
	orchCfg.RateInterval = cfg.AI.KeyframeRateInterval
	if ratio := cfg.AI.VeoAspectRatio; ratio != "" {
		orchCfg.KeyframeAspectRatio = ratio
	}
	if size := cfg.AI.KeyframeImageSize; size != "" {
		orchCfg.KeyframeImageSize = size
	}
	orchCfg.ApplyDefaults()
	return orchCfg
}
