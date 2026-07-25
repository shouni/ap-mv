// Package logging は、Cloud Logging が解釈できる構造化ログを出力するロガーを構成します。
//
// slog の既定 JSON 出力は `level`/`msg` キーを使いますが、Cloud Logging が参照するのは
// `severity`/`message` です。既定のままだと Logs Explorer 上ですべてのエントリが INFO 扱いになり、
// 重大度での絞り込みができません。ここで属性名を詰め替えることでその差を吸収します。
//
// このパッケージは姉妹プロジェクト ap-comp にも複製されています。共有ライブラリへ切り出す場合は
// 汎用部分（ParseLevel・With・contextHandler）を go-utils へ、GCP 固有部分（severity 詰め替え・
// TraceMiddleware）を gcp-kit へ分ける必要があります。分割しないと、ベンダー依存を持たない
// worker パイプライン層が GCP ライブラリを参照することになり、層の境界が崩れるためです。
// ログ形式はサービス間の契約ではなく重複しても不整合バグを生まないため、
// 3 つ目の利用者が出るまでは複製で運用します。
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// LevelEnvKey は出力レベルを指定する環境変数名です。
const LevelEnvKey = "LOG_LEVEL"

// TraceKey / SpanKey は Cloud Logging がトレースとの相関に使う予約フィールド名です。
const (
	TraceKey = "logging.googleapis.com/trace"
	SpanKey  = "logging.googleapis.com/spanId"
)

// Setup は LOG_LEVEL に従ったロガーを構築し、既定ロガーとして設定します。
func Setup() *slog.Logger {
	logger := New(os.Stdout, ParseLevel(os.Getenv(LevelEnvKey)))
	slog.SetDefault(logger)
	return logger
}

// New は Cloud Logging 互換の JSON ロガーを構築します。
// 返されるロガーは context に積まれた属性（job_id やトレース ID）を自動で付与するため、
// 既存の slog.XxxContext 呼び出しをそのまま相関ログにできます。
func New(w io.Writer, level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	})
	return slog.New(&contextHandler{Handler: handler})
}

// ParseLevel は環境変数の文字列を slog のレベルへ変換します。未知の値は Info とみなします。
func ParseLevel(raw string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// replaceAttr は slog の標準キーを Cloud Logging の予約フィールドへ詰め替えます。
// グループ内の属性はアプリ固有のデータなので触りません。
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}

	switch a.Key {
	case slog.LevelKey:
		a.Key = "severity"
		if level, ok := a.Value.Any().(slog.Level); ok {
			a.Value = slog.StringValue(severityOf(level))
		}
	case slog.MessageKey:
		a.Key = "message"
	}
	return a
}

// severityOf は slog のレベルを Cloud Logging の severity 文字列へ対応付けます。
func severityOf(level slog.Level) string {
	switch {
	case level < slog.LevelInfo:
		return "DEBUG"
	case level < slog.LevelWarn:
		return "INFO"
	case level < slog.LevelError:
		return "WARNING"
	default:
		return "ERROR"
	}
}

type logAttrsContextKey struct{}

// With は以降のログすべてに付与される属性を context に積みます。
// リクエスト単位のトレース ID やジョブ単位の job_id を、各ログ呼び出しへ引数として
// 配って回らずに相関させるための仕組みです。
func With(ctx context.Context, attrs ...slog.Attr) context.Context {
	if len(attrs) == 0 {
		return ctx
	}

	existing := attrsFrom(ctx)
	merged := make([]slog.Attr, 0, len(existing)+len(attrs))
	merged = append(merged, existing...)
	merged = append(merged, attrs...)
	return context.WithValue(ctx, logAttrsContextKey{}, merged)
}

// attrsFrom は context に積まれた属性を返します。
func attrsFrom(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	attrs, _ := ctx.Value(logAttrsContextKey{}).([]slog.Attr)
	return attrs
}

// contextHandler は context に積まれた属性をレコードへ付与する slog.Handler です。
type contextHandler struct {
	slog.Handler
}

// Handle は context 由来の属性を足したうえで委譲先のハンドラーへ渡します。
func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	if attrs := attrsFrom(ctx); len(attrs) > 0 {
		record.AddAttrs(attrs...)
	}
	return h.Handler.Handle(ctx, record)
}

// WithAttrs / WithGroup は委譲先を包み直し、context 属性の付与を維持します。
func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithGroup(name)}
}
