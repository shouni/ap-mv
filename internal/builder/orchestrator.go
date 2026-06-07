package builder

import (
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"ap-mv/internal/config"
)

func buildOrchestratorConfig(cfg *config.Config) orchestrator.Config {
	orchCfg := orchestrator.Config{}
	if cfg == nil {
		orchCfg.ApplyDefaults()
		return orchCfg
	}

	orchCfg.GeminiModel = cfg.GeminiModel
	orchCfg.ImageModel = cfg.ImageModel
	orchCfg.ApplyDefaults()
	return orchCfg
}
