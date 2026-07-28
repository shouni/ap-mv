package domain

import (
	"sort"
	"strings"
	"time"
)

// VeoUsageFileName は、ジョブの出力ディレクトリに置く Veo 生成実績のファイル名です。
// video_music_meta.json とは別ファイルにしています。メタデータ本体は
// go-veo-orchestrator の VideoRecipe が構造を決めており、実績を足すにはあちらのリリースが
// 要るためです。用途も寿命も違う（片方は完成品の記述、片方は課金の記録）ので、
// 分けておくほうが読み手にも分かりやすくなります。
const VeoUsageFileName = "veo_usage.json"

// VeoUsageSchemaVersion は veo_usage.json のスキーマ版です。読み手が将来の形式変更を
// 判別できるよう、最初から埋めておきます。
const VeoUsageSchemaVersion = 1

// VeoCutUsage は、1カットに対して実際に走った Veo 生成の実績です。
// Calls が 2 以上なら、そのカットは焼き直されています（Cloud Tasks の再配信、
// セクション再生成など）。
type VeoCutUsage struct {
	CutIndex int `json:"cut_index"`
	// Calls は成功した生成回数です。失敗した呼び出しは（通常課金されないため）数えません。
	Calls int `json:"calls"`
	// SubmittedSeconds は成功した生成の尺の合計です。同じカットを2回焼けば尺は2回分入ります。
	SubmittedSeconds float64 `json:"submitted_seconds"`
	// LastGeneratedAt は最後に成功した生成の時刻です。
	LastGeneratedAt time.Time `json:"last_generated_at,omitzero"`
}

// VeoUsage は、1ジョブで実際に Veo へ投げた生成の実績です。
//
// VideoRecipe から算出できる「完成品の尺」と違い、こちらは投げた回数そのものを積み上げます。
// 2つの差が、再生成で捨てた分になります。
//
// 正確な会計帳簿ではありません。1カット生成するたびに read-modify-write するので、
// 同じジョブが同時に2つ走ると更新を取りこぼす可能性があります（その場合は実績を過小に
// 数えます）。生成そのものを止めないことを優先しており、記録の失敗はログに残して先へ進みます。
type VeoUsage struct {
	SchemaVersion int    `json:"schema_version"`
	JobID         string `json:"job_id,omitempty"`
	// Model は生成に使った Veo モデルです。タスクがモデルを明示しなかった場合は空になり、
	// 読み手は表示時点の既定モデルで代用します。
	Model string `json:"model,omitempty"`
	// Calls は成功した生成の総回数です。
	Calls int `json:"calls"`
	// SubmittedSeconds は成功した生成の尺の総合計（＝実際の課金対象秒数の概算）です。
	SubmittedSeconds float64       `json:"submitted_seconds"`
	Cuts             []VeoCutUsage `json:"cuts,omitempty"`
	UpdatedAt        time.Time     `json:"updated_at,omitzero"`
}

// Record は、成功した1回の生成を実績へ加算します。
// model が空でないときだけ記録を上書きするので、モデル指定のあるタスクとないタスクが
// 混ざっても、判明している値が空で潰れることはありません。
func (u *VeoUsage) Record(cutIndex int, durationSec float64, model string, now time.Time) {
	if u == nil {
		return
	}
	if u.SchemaVersion == 0 {
		u.SchemaVersion = VeoUsageSchemaVersion
	}
	if model = strings.TrimSpace(model); model != "" {
		u.Model = model
	}
	u.Calls++
	u.SubmittedSeconds += durationSec
	u.UpdatedAt = now

	for i := range u.Cuts {
		if u.Cuts[i].CutIndex == cutIndex {
			u.Cuts[i].Calls++
			u.Cuts[i].SubmittedSeconds += durationSec
			u.Cuts[i].LastGeneratedAt = now
			return
		}
	}
	u.Cuts = append(u.Cuts, VeoCutUsage{
		CutIndex:         cutIndex,
		Calls:            1,
		SubmittedSeconds: durationSec,
		LastGeneratedAt:  now,
	})
	sort.Slice(u.Cuts, func(i, j int) bool { return u.Cuts[i].CutIndex < u.Cuts[j].CutIndex })
}

// CutCalls は指定カットの成功生成回数を返します。記録が無ければ 0 を返します。
func (u *VeoUsage) CutCalls(cutIndex int) int {
	if u == nil {
		return 0
	}
	for _, cut := range u.Cuts {
		if cut.CutIndex == cutIndex {
			return cut.Calls
		}
	}
	return 0
}
