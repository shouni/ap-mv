package builder

import (
	"fmt"

	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/ap-mv/internal/app"
)

// buildRemoteIO は、I/O コンポーネントを初期化します。
func buildRemoteIO(storage remoteio.IOFactory) (*app.RemoteIO, error) {
	if storage == nil {
		return nil, fmt.Errorf("storage factory cannot be nil")
	}

	r, err := storage.InputReader()
	if err != nil {
		return nil, fmt.Errorf("failed to create input reader: %w", err)
	}
	w, err := storage.OutputWriter()
	if err != nil {
		return nil, fmt.Errorf("failed to create output writer: %w", err)
	}
	s, err := storage.URLSigner()
	if err != nil {
		return nil, fmt.Errorf("failed to create URL signer: %w", err)
	}
	return &app.RemoteIO{
		Factory: storage,
		Reader:  r,
		Writer:  w,
		Signer:  s,
	}, nil
}
