package domain

import (
	"strconv"
	"strings"
)

// JobStage は、ジョブが生成のどの段階まで進んだかを表します。
//
// 以前は「全カットが動画生成済みか」という真偽値ひとつしか無く、表示は generated か
// keyframes の 2 値でした。そのため「キーフレームを 1 枚も焼いていないジョブ」と
// 「動画をあと 1 本残すだけのジョブ」が同じ表示になり、進捗も残作業も読めませんでした。
// カット単位で少しずつ進める運用では、そこが見えないまま課金だけが進みます。
type JobStage string

const (
	// StageScript はカット割りまで決まり、キーフレームをまだ 1 枚も焼いていない状態です。
	StageScript JobStage = "script"
	// StageKeyframes はキーフレームを焼いている途中（1 枚以上、全部ではない）です。
	StageKeyframes JobStage = "keyframes"
	// StageKeyframesDone は全カットのキーフレームが揃い、動画をまだ 1 本も作っていない状態です。
	StageKeyframesDone JobStage = "keyframes_done"
	// StageVideos は動画を生成している途中（1 本以上、全部ではない）です。
	StageVideos JobStage = "videos"
	// StageCompleted は全カットの動画生成が終わった状態です。
	StageCompleted JobStage = "completed"
)

// JobProgress は、ジョブの進行段階と、その根拠になる数え上げです。
//
// 画面は Stage でバッジの色を決め、分数（3/12 など）で残りを示します。合計が 0 の
// ジョブ（カットが 1 つも無いレシピ）は Stage が StageScript になります。
type JobProgress struct {
	Stage JobStage `json:"stage"`
	// TotalCuts はレシピのカット総数です。
	TotalCuts int `json:"total_cuts"`
	// KeyframeCuts はキーフレーム画像を持つカット数です。
	KeyframeCuts int `json:"keyframe_cuts"`
	// VideoCuts は動画生成が終わったカット数です。
	VideoCuts int `json:"video_cuts"`
}

// IsCompleted は全カットの動画生成が終わっているかを返します。
// 旧 VideoHistory.Generated と同じ意味で、既存の分岐はこれに置き換えられます。
func (p JobProgress) IsCompleted() bool {
	return p.Stage == StageCompleted
}

// Label は画面に出す短い進捗表記を返します（例: "keyframes 3/12", "completed"）。
// 段階が分かるだけでなく残りが読めるよう、途中の段階では分数を添えます。
func (p JobProgress) Label() string {
	switch p.Stage {
	case StageKeyframes:
		return "keyframes " + strconv.Itoa(p.KeyframeCuts) + "/" + strconv.Itoa(p.TotalCuts)
	case StageVideos:
		return "videos " + strconv.Itoa(p.VideoCuts) + "/" + strconv.Itoa(p.TotalCuts)
	case StageKeyframesDone:
		return "keyframes done"
	default:
		return string(p.Stage)
	}
}

// NewJobProgress は、レシピのカット列から進行段階を数え上げます。
//
// キーフレームの有無は KeyframeReference、動画の完了は Cut.IsGenerated() で見ます。
// 動画が 1 本でもあればキーフレームは必ず存在するため、段階は上から順に判定します。
func NewJobProgress(cuts []VideoCut) JobProgress {
	progress := JobProgress{Stage: StageScript, TotalCuts: len(cuts)}
	for _, cut := range cuts {
		if strings.TrimSpace(cut.KeyframeReference) != "" {
			progress.KeyframeCuts++
		}
		if cut.IsGenerated() {
			progress.VideoCuts++
		}
	}

	switch {
	case progress.TotalCuts == 0:
		// カットが無いレシピは台本だけの状態として扱う（0/0 を「完了」と読ませない）。
		progress.Stage = StageScript
	case progress.VideoCuts == progress.TotalCuts:
		progress.Stage = StageCompleted
	case progress.VideoCuts > 0:
		progress.Stage = StageVideos
	case progress.KeyframeCuts == progress.TotalCuts:
		progress.Stage = StageKeyframesDone
	case progress.KeyframeCuts > 0:
		progress.Stage = StageKeyframes
	default:
		progress.Stage = StageScript
	}
	return progress
}

// TotalDurationSecOfCuts は全カットの尺の合計を返します。GeneratedSecondsOfCuts と違い
// 生成済みかどうかを問わないため、まだ 1 カットも生成していないジョブでも使えます。
func TotalDurationSecOfCuts(cuts []VideoCut) float64 {
	total := 0.0
	for _, cut := range cuts {
		total += cut.DurationSec
	}
	return total
}
