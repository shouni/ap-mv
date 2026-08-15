package pipeline

import (
	"context"
	"testing"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
)

// 後始末（release）のテストは削除しました。go-veo-orchestrator が参照画像を gs:// のまま
// 渡すようになり Workflows.Close が消えたため、Resolve が返していた release 自体が
// 契約から無くなっています。

// TestStaticWorkflowResolverReturnsConfiguredWorkflows は、テスト用の共有リゾルバが
// 設定された Workflows をそのまま返すことを確認します。
func TestStaticWorkflowResolverReturnsConfiguredWorkflows(t *testing.T) {
	workflows := &orchestrator.Workflows{}

	got, err := StaticWorkflowResolver{Workflows: workflows}.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != workflows {
		t.Error("Resolve() did not return the configured workflows")
	}
}
