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
// 後始末はありません。以前は解決した Workflows を Close する release を返していましたが、
// go-veo-orchestrator が参照画像を gs:// のまま渡すようになり、停止すべき画像キャッシュの
// goroutine が無くなったため、Workflows から Close 自体が消えました。
type WorkflowResolver interface {
	Resolve(context.Context, *domain.Task) (*orchestrator.Workflows, error)
}

// StaticWorkflowResolver は、常に固定の Workflows を返すテスト用の WorkflowResolver です。
type StaticWorkflowResolver struct {
	Workflows *orchestrator.Workflows
}

// Resolve は保持している Workflows をそのまま返します。
func (r StaticWorkflowResolver) Resolve(context.Context, *domain.Task) (*orchestrator.Workflows, error) {
	return r.Workflows, nil
}
