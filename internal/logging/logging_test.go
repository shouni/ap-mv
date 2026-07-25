package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeLines は出力された JSON ログを 1 行ずつマップへ復号します。
func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var entries []map[string]any
	decoder := json.NewDecoder(buf)
	for decoder.More() {
		var entry map[string]any
		require.NoError(t, decoder.Decode(&entry))
		entries = append(entries, entry)
	}
	return entries
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		raw  string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		// 環境変数は前後に空白が混ざりやすいため、トリムされること。
		{" warn ", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"ERROR", slog.LevelError},
		{"", slog.LevelInfo},
		{"nonsense", slog.LevelInfo},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, ParseLevel(tt.raw), "ParseLevel(%q)", tt.raw)
	}
}

// Cloud Logging は level/msg ではなく severity/message を読むため、詰め替えが必須。
func TestNewWritesCloudLoggingFields(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)

	logger.Warn("something happened", "job_id", "20260725-abcd1234")

	entries := decodeLines(t, &buf)
	require.Len(t, entries, 1)
	assert.Equal(t, "WARNING", entries[0]["severity"])
	assert.Equal(t, "something happened", entries[0]["message"])
	assert.Equal(t, "20260725-abcd1234", entries[0]["job_id"])
	assert.NotContains(t, entries[0], "level")
	assert.NotContains(t, entries[0], "msg")
}

func TestNewRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, slog.LevelWarn).Info("filtered out")
	assert.Empty(t, buf.String())

	buf.Reset()
	New(&buf, slog.LevelDebug).Debug("emitted")
	assert.Len(t, decodeLines(t, &buf), 1)
}

func TestWithAddsContextAttrsToEveryRecord(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)

	ctx := With(context.Background(), slog.String("job_id", "job-1"))
	ctx = With(ctx, slog.String("command", "compose"))

	logger.InfoContext(ctx, "phase started")
	logger.InfoContext(context.Background(), "unrelated")

	entries := decodeLines(t, &buf)
	require.Len(t, entries, 2)
	assert.Equal(t, "job-1", entries[0]["job_id"])
	assert.Equal(t, "compose", entries[0]["command"])
	assert.NotContains(t, entries[1], "job_id")
}

// With は元の context の属性スライスを共有せず、分岐しても互いに汚染しないこと。
func TestWithDoesNotLeakBetweenBranches(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)

	base := With(context.Background(), slog.String("job_id", "job-1"))
	logger.InfoContext(With(base, slog.String("phase", "collect")), "a")
	logger.InfoContext(With(base, slog.String("phase", "publish")), "b")

	entries := decodeLines(t, &buf)
	require.Len(t, entries, 2)
	assert.Equal(t, "collect", entries[0]["phase"])
	assert.Equal(t, "publish", entries[1]["phase"])
}

// WithAttrs で包み直したあとも context 由来の属性が消えないこと。
func TestContextAttrsSurviveLoggerWith(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo).With("component", "pipeline")

	logger.InfoContext(With(context.Background(), slog.String("job_id", "job-1")), "msg")

	entries := decodeLines(t, &buf)
	require.Len(t, entries, 1)
	assert.Equal(t, "pipeline", entries[0]["component"])
	assert.Equal(t, "job-1", entries[0]["job_id"])
}

func TestParseCloudTraceContext(t *testing.T) {
	tests := []struct {
		header    string
		wantTrace string
		wantSpan  string
	}{
		{"abc123/456;o=1", "abc123", "456"},
		{"abc123/456", "abc123", "456"},
		{"abc123", "abc123", ""},
		{"", "", ""},
	}

	for _, tt := range tests {
		trace, span := parseCloudTraceContext(tt.header)
		assert.Equal(t, tt.wantTrace, trace, "trace for %q", tt.header)
		assert.Equal(t, tt.wantSpan, span, "span for %q", tt.header)
	}
}

func TestTraceMiddlewareAttachesTrace(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)

	handler := TraceMiddleware("my-project")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		logger.InfoContext(r.Context(), "handled")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(CloudTraceHeader, "trace-abc/span-1;o=1")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	entries := decodeLines(t, &buf)
	require.Len(t, entries, 1)
	assert.Equal(t, "projects/my-project/traces/trace-abc", entries[0][TraceKey])
	assert.Equal(t, "span-1", entries[0][SpanKey])
}

// projectID 未設定（ローカル実行）では完全修飾名を組めないため何も付与しない。
func TestTraceMiddlewareSkipsWithoutProjectID(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)

	handler := TraceMiddleware("")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		logger.InfoContext(r.Context(), "handled")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(CloudTraceHeader, "trace-abc/span-1;o=1")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	entries := decodeLines(t, &buf)
	require.Len(t, entries, 1)
	assert.NotContains(t, entries[0], TraceKey)
}
