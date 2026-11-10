package builder

import (
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/adapters/prompt"
	"github.com/shouni/ap-mv/internal/config"
	"github.com/shouni/ap-mv/internal/domain"
)

// buildOrchestratorConfig builds orchestrator config.
//
// go-veo-orchestrator has no defaults for aspect ratio, resolution or the negative prompt, so this
// app is their only source (if the kit also had defaults it and VEO_ASPECT_RATIO would be two
// sources, and changing only one would render just the keyframes at a different ratio).
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
