package ports

import (
	"context"
	"io"

	"github.com/shouni/ap-mv/internal/domain"
)

// Pipeline is the boundary that runs worker tasks and can release the run-time resources it holds.
type Pipeline interface {
	Execute(context.Context, domain.Task) error
	io.Closer
}
