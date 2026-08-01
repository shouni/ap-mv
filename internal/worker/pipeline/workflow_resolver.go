package pipeline

import (
	"context"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
)

// WorkflowResolver は、タスクに応じて使用する orchestrator Workflows を解決します。
// 「共有 Workflows を再利用できるか、タスク固有オプション（モデル・シード・Veo 設定の
// 上書き）のためにタスク専用の Workflows を構築すべきか」の判定と構築は実装側
// （builder.workflowResolver）に集約され、Runner は解決結果を使うだけです。
//
// 戻り値の release は、解決した Workflows の後始末です。タスク専用に構築した場合は
// その Workflows を Close し（画像キャッシュのバックグラウンド goroutine が止まります）、
// 共有 Workflows を返した場合は何もしません。呼び出し側は解決に成功したら必ず呼びます。
// 「共有か専用か」を呼び出し側が判定できない以上、後始末は解決した側が返すのが唯一
// 安全な形です。
type WorkflowResolver interface {
	Resolve(context.Context, *domain.Task) (workflows *orchestrator.Workflows, release func(), err error)
}

// StaticWorkflowResolver は、常に固定の Workflows を返すテスト用の WorkflowResolver です。
type StaticWorkflowResolver struct {
	Workflows *orchestrator.Workflows
}

// Resolve は保持している Workflows をそのまま返します。共有インスタンスなので
// release は何もしません。
func (r StaticWorkflowResolver) Resolve(context.Context, *domain.Task) (*orchestrator.Workflows, func(), error) {
	return r.Workflows, func() {}, nil
}
