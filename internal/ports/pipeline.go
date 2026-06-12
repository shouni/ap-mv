package ports

import (
	"context"
	"io"

	"ap-mv/internal/domain"
)

// Pipeline は worker task を実行し、保持する実行時リソースを解放できる境界です。
type Pipeline interface {
	Execute(context.Context, domain.Task) error
	io.Closer
}
