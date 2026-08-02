package repository

import (
	"testing"

	"github.com/shouni/go-job-kit/paging"
	"github.com/shouni/go-utils/jobid"
)

// 用途プレフィックスが混在しても作成日時の降順に並ぶこと。
// ID の文字列比較だとプレフィックス順（video-recipe- が先頭）になってしまいます。
// ソートキーは paging.WithSortKey に渡されるため、ここが壊れると一覧が
// エラーにならず静かに並び替わります。
//
// タイムスタンプの抽出そのものは go-utils/jobid 側で検証済みです。
// ここでは paging との結線と、他サービス採番の ID が混ざった場合を確認します。
func TestHistorySortKeyOrdersByCreatedAtDesc(t *testing.T) {
	jobIDs := []string{
		"video-recipe-20260706-194856-aaa",
		"mv-20260711-010101-bbb",
		"short-20260710-155126-ccc",
		"20260712235959-ddd", // 他サービス採番（日付と時刻が分割されない形式）
		"no-timestamp-job",
	}

	selected, meta := paging.SelectIDs(jobIDs, 1, 10, paging.WithSortKey(jobid.SortKey))

	want := []string{
		"20260712235959-ddd",
		"mv-20260711-010101-bbb",
		"short-20260710-155126-ccc",
		"video-recipe-20260706-194856-aaa",
		"no-timestamp-job",
	}
	if len(selected) != len(want) {
		t.Fatalf("selected = %d ids, want %d", len(selected), len(want))
	}
	for i := range want {
		if selected[i] != want[i] {
			t.Fatalf("selected[%d] = %q, want %q (got order %v)", i, selected[i], want[i], selected)
		}
	}
	if meta.Total != len(want) {
		t.Fatalf("meta.Total = %d, want %d", meta.Total, len(want))
	}
}

// TestFormatHistoryCreatedAt は、UTC 採番の ID が JST で表示されることを確認します。
// 実行環境のタイムゾーンに依存しないことが要件のため、CI（UTC）でも通る必要があります。
func TestFormatHistoryCreatedAt(t *testing.T) {
	tests := []struct {
		name  string
		jobID string
		want  string
	}{
		{"New が生成する形式", "video-recipe-20260706-194856-efeeccfc3b0c", "2026-07-07 04:48 JST"},
		{"他サービス採番の形式", "20260706194856-abcd", "2026-07-07 04:48 JST"},
		{"時刻を持たない ID", "no-timestamp", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatHistoryCreatedAt(tt.jobID); got != tt.want {
				t.Errorf("formatHistoryCreatedAt(%q) = %q, want %q", tt.jobID, got, tt.want)
			}
		})
	}
}
