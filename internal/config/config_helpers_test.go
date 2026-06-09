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
	tests := map[string]string{
		"":                           "https://example.com/tasks/generate",
		" https://worker/tasks/run ": "https://worker/tasks/run",
	}

	for input, want := range tests {
		if got := normalizeWorkerURL("https://example.com/", input); got != want {
			t.Fatalf("normalizeWorkerURL(%q) = %q, want %q", input, got, want)
		}
	}
}
