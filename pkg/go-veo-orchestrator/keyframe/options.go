package keyframe

import "time"

// KeyframeOption は KeyframeGenerator の設定を適用する関数型です。
type KeyframeOption func(*KeyframeGenerator)

// WithKeyframeMaxConcurrency は、キーフレーム生成の最大並列数を設定します。
func WithKeyframeMaxConcurrency(value int) KeyframeOption {
	return func(g *KeyframeGenerator) {
		if value > 0 {
			g.maxConcurrency = value
		}
	}
}

// WithKeyframeRateInterval は、キーフレーム生成のレートリミット間隔を設定します。
func WithKeyframeRateInterval(d time.Duration) KeyframeOption {
	return func(g *KeyframeGenerator) {
		if d > 0 {
			g.rateInterval = d
		}
	}
}

// WithKeyframeRateBurst は、キーフレーム生成のバースト許容数を設定します。
func WithKeyframeRateBurst(value int) KeyframeOption {
	return func(g *KeyframeGenerator) {
		if value > 0 {
			g.rateBurst = value
		}
	}
}
