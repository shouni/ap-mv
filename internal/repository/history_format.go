package repository

import (
	"github.com/shouni/go-utils/jobid"
	"github.com/shouni/go-utils/jst"
)

// formatHistoryCreatedAt は、jobID に埋め込まれた作成時刻を履歴表示用の文字列にします。
// 時刻を取り出せない ID では空文字を返し、一覧では日時なしとして扱われます。
//
// 採番は UTC、JST への変換は表示の直前だけです。実行環境のタイムゾーン設定
// （コンテナの TZ など）には依存しません。
func formatHistoryCreatedAt(jobID string) string {
	createdAt, err := jobid.CreatedAt(jobID)
	if err != nil {
		return ""
	}
	return jst.Format(createdAt, jst.LayoutDisplay)
}
