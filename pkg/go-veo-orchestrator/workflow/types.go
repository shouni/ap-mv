package workflow

import (
	"time"
)

const (
	// defaultCacheExpiration は、インメモリに保持されたアセット情報の有効期限です。
	// フェッチ済みの画像バイナリや、既に Gemini File API にアップロード済みの
	// URI 情報を再利用し、重複した I/O やアップロード処理を抑制します。
	defaultCacheExpiration = 5 * time.Minute

	// defaultTTL は、リモートリソース（GCSや外部URL）の有効期間です。
	// 短期間に同じ参照URIが要求された際、ソースへの再アクセスを防ぐために使用されます。
	defaultTTL = 5 * time.Minute
)
