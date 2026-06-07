package builder

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"
)

type workflowReader struct {
	delegate remoteio.InputReader
}

func (r workflowReader) Open(ctx context.Context, uri string) (io.ReadCloser, error) {
	if strings.HasPrefix(uri, "data:") {
		return openDataURI(uri)
	}
	if r.delegate == nil {
		return nil, fmt.Errorf("reader is not configured")
	}
	return r.delegate.Open(ctx, uri)
}

func openDataURI(raw string) (io.ReadCloser, error) {
	header, payload, ok := strings.Cut(raw, ",")
	if !ok {
		return nil, fmt.Errorf("invalid data URI")
	}
	var data []byte
	var err error
	if strings.HasSuffix(header, ";base64") || strings.Contains(header, ";base64;") {
		data, err = base64.StdEncoding.DecodeString(payload)
	} else {
		var decoded string
		decoded, err = url.QueryUnescape(payload)
		data = []byte(decoded)
	}
	if err != nil {
		return nil, fmt.Errorf("decode data URI: %w", err)
	}
	return io.NopCloser(strings.NewReader(string(data))), nil
}
