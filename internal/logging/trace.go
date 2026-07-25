package logging

import (
	"log/slog"
	"net/http"
	"strings"
)

// CloudTraceHeader は Cloud Run / Cloud Load Balancing が付与するトレースヘッダーです。
const CloudTraceHeader = "X-Cloud-Trace-Context"

// TraceMiddleware は X-Cloud-Trace-Context を解析し、以降のログへトレース ID を付与します。
// これにより Logs Explorer 上でリクエスト単位にログがまとまります。
// projectID が空の場合、Cloud Logging がトレースと紐付けられる完全修飾名を組み立てられないため、
// 何もせず素通しします（ローカル実行時など）。
func TraceMiddleware(projectID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if strings.TrimSpace(projectID) == "" {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceID, spanID := parseCloudTraceContext(r.Header.Get(CloudTraceHeader))
			if traceID == "" {
				next.ServeHTTP(w, r)
				return
			}

			attrs := []slog.Attr{
				slog.String(TraceKey, "projects/"+projectID+"/traces/"+traceID),
			}
			if spanID != "" {
				attrs = append(attrs, slog.String(SpanKey, spanID))
			}
			next.ServeHTTP(w, r.WithContext(With(r.Context(), attrs...)))
		})
	}
}

// parseCloudTraceContext は "TRACE_ID/SPAN_ID;o=1" 形式のヘッダーを分解します。
func parseCloudTraceContext(header string) (traceID string, spanID string) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", ""
	}

	traceID, remainder, found := strings.Cut(header, "/")
	if !found {
		return traceID, ""
	}
	spanID, _, _ = strings.Cut(remainder, ";")
	return traceID, spanID
}
