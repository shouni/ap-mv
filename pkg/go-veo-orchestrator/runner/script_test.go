package runner

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestReadContentRemovesInvalidUTF8Anywhere(t *testing.T) {
	r := &VideoScriptRunner{
		reader: staticContentReader{content: []byte{'a', 0xff, 'b'}},
	}

	got, err := r.readContent(context.Background(), "memory://invalid-utf8")
	if err != nil {
		t.Fatalf("readContent() error = %v", err)
	}
	if got != "ab" {
		t.Fatalf("readContent() = %q, want %q", got, "ab")
	}
}

func TestExtractJSONStringSupportsArrayRoot(t *testing.T) {
	raw := "prefix [{\"id\":1}] suffix"

	got := extractJSONString(raw)
	if got != "[{\"id\":1}]" {
		t.Fatalf("extractJSONString() = %q", got)
	}
}

func TestExtractJSONStringSupportsObjectRoot(t *testing.T) {
	raw := "prefix {\"id\":1} suffix"

	got := extractJSONString(raw)
	if got != "{\"id\":1}" {
		t.Fatalf("extractJSONString() = %q", got)
	}
}

type staticContentReader struct {
	content []byte
}

func (r staticContentReader) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(string(r.content))), nil
}
