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
