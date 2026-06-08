package keyframe

import (
	"strings"
	"time"
)

const (
	// CutAspectRatio は単体カットのキーフレーム推奨アスペクト比です。
	CutAspectRatio = "16:9"

	// ImageSize1K は標準的な解像度の設定（1024x1024相当）です。
	ImageSize1K = "1K"
	// ImageSize2K は高解像度の設定（2048x2048相当）です。
	ImageSize2K = "2K"
	// ImageSize4K は超高解像度の設定（4096x4096相当）です。
	ImageSize4K = "4K"

	// defaultRateBurst は、短時間に許容される最大リクエスト数（バースト）です。
	// API のレート制限（429 Too Many Requests）に抵触しないよう制御します。
	defaultRateBurst = 1
	// DefaultRateInterval は、リクエスト間のデフォルトの待機間隔です。
	defaultRateInterval = 60 * time.Second
)

// IsGCSURI は、指定されたURIがGCS（Google Cloud Storage）のストレージURIであるかどうかを判定します。
func IsGCSURI(uri string) bool {
	const prefixGCS = "gs://"
	return strings.HasPrefix(uri, prefixGCS)
}
