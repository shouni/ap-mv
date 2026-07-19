package builder

import (
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/config"
)

// buildOrchestratorConfig builds orchestrator config.
func buildOrchestratorConfig(cfg *config.Config) orchestrator.Config {
	orchCfg := orchestrator.Config{}
	if cfg == nil {
		orchCfg.ApplyDefaults()
		return orchCfg
	}

	orchCfg.GeminiModel = cfg.AI.GeminiModel
	orchCfg.ImageModel = cfg.AI.ImageModel
	orchCfg.MaxConcurrency = cfg.AI.KeyframeMaxConcurrency
	orchCfg.RateInterval = cfg.AI.KeyframeRateInterval
	orchCfg.ApplyDefaults()
	return orchCfg
}
