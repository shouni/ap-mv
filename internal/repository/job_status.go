package repository

import (
	"cloud.google.com/go/firestore"
	"github.com/shouni/gcp-kit/jobstatus"

	"github.com/shouni/ap-mv/internal/domain"
)

// statusCollection は、ジョブ状態を置く Firestore のコレクションです。
//
// 成果物のバケットと同じ語彙にしてあります。コレクションはサービスごとに 1 本で、
// 共有 1 本に判別フィールドを持たせる形は採りません（絞り忘れが落ちずに他サービスの
// ジョブを履歴へ混ぜるためです）。設定にせず定数なのは、これがサービスの身元であって
// デプロイごとに変わる値ではないからです。
const statusCollection = "ap-mv"

// NewJobStatusRepository は、Firestore を裏付けとしたジョブ進行状況の読み書きを構築します。
//
// 保存形式・ジョブ ID の正規化・エラーの仕分けは gcp-kit の jobstatus が担います。
// ports.JobStatusStore は jobstatus.Store と同じシグネチャなので、包む型は要りません。
// ドキュメントは常に最新の 1 世代だけを保持し、上書きで更新します。
//
// 状態は成果物と別の場所にあるため、履歴削除（プレフィックス一括削除）では消えません。
// ドキュメントを消すのはハンドラー側の仕事です（handlers.deleteJobStatus）。
func NewJobStatusRepository(client *firestore.Client) *jobstatus.Store[domain.JobStatus] {
	return jobstatus.NewStore[domain.JobStatus](client, statusCollection)
}

// Recorder.Begin は、Store が Modifier を満たすことを型で見つけ、再実行ガードと
// running の記録を 1 つの Firestore トランザクションで行います。Store を包んだり
// gcp-kit を v1.21.0 より前へ下げたりすると、ここでコンパイルが止まります。満たさない
// まま動くと、ガードは黙って Get と Save が別々の（割り込みうる）経路へ戻ります。
var _ jobstatus.Modifier[domain.JobStatus] = (*jobstatus.Store[domain.JobStatus])(nil)
