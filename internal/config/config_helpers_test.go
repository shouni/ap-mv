package config

import "testing"

func TestNormalizeGCSBucket(t *testing.T) {
	tests := map[string]string{
		"my-bucket":          "my-bucket",
		" gs://my-bucket ":   "my-bucket",
		"gs://my-bucket///":  "my-bucket",
		"/my-bucket/":        "my-bucket",
		"gs://my-bucket/out": "my-bucket/out",
	}

	for input, want := range tests {
		if got := normalizeGCSBucket(input); got != want {
			t.Fatalf("normalizeGCSBucket(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeWorkerURL(t *testing.T) {
	tests := []struct {
		name    string
		service string
		worker  string
		want    string
	}{
		{
			name:    "default",
			service: "https://example.com/",
			want:    "https://example.com/tasks/generate",
		},
		{
			name:    "explicit worker",
			service: "https://example.com/",
			worker:  " https://worker/tasks/run ",
			want:    "https://worker/tasks/run",
		},
		{
			name:    "base path",
			service: "https://example.com/base",
			want:    "https://example.com/base/tasks/generate",
		},
		{
			name:    "query",
			service: "https://example.com?q=1",
			want:    "https://example.com/tasks/generate?q=1",
		},
		{
			name:    "fragment",
			service: "https://example.com#frag",
			want:    "https://example.com/tasks/generate#frag",
		},
		{
			name:    "trailing slash with base path",
			service: "https://example.com/base/",
			want:    "https://example.com/base/tasks/generate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeWorkerURL(tt.service, tt.worker); got != tt.want {
				t.Fatalf("normalizeWorkerURL(%q, %q) = %q, want %q", tt.service, tt.worker, got, tt.want)
			}
		})
	}
}
