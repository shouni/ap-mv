package handlers

import (
	"context"

	"github.com/shouni/gcp-kit/auth"
)

// csrfTokenFromContext は、コンテキストに保存された CSRF トークンを取得します。
//
// 格納するのは gcp-kit/auth の CSRFContextMiddleware なので、取得も同じキーを
// 見る必要があります。ここで委譲しているのは、テンプレート描画側から見た呼び名を
// handlers パッケージに残すためだけです。
func csrfTokenFromContext(ctx context.Context) string {
	return auth.CSRFTokenFromContext(ctx)
}
