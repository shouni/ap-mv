package ports

import veoports "github.com/shouni/go-veo-orchestrator/ports"

// VideoRunner は go-veo-orchestrator が定義する Veo adapter 境界です。
type VideoRunner = veoports.VideoRunner

// VideoGenerationRequest は go-veo-orchestrator の動画生成リクエスト型です。
type VideoGenerationRequest = veoports.VideoGenerationRequest

// VideoResponse は go-veo-orchestrator の動画生成レスポンス型です。
type VideoResponse = veoports.VideoResponse
