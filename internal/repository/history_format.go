package repository

import (
	"strings"
	"time"
)

func formatHistoryCreatedAt(jobID string) string {
	const displayTimeLayout = "2006-01-02 15:04 MST"
	jst := time.FixedZone("JST", 9*60*60)

	raw := historyCreatedAtRaw(jobID)
	if raw == "" {
		return ""
	}
	createdAt, err := time.ParseInLocation("20060102150405", raw, time.UTC)
	if err != nil {
		return ""
	}
	return createdAt.In(jst).Format(displayTimeLayout)
}

// historyCreatedAtRaw は jobID に埋め込まれた作成タイムスタンプ（"20060102150405" 形式）を
// 返します。見つからない場合は空文字を返します。ソートキーとしてそのまま比較できます。
func historyCreatedAtRaw(jobID string) string {
	parts := strings.Split(jobID, "-")
	for i := 0; i+1 < len(parts); i++ {
		raw := parts[i] + parts[i+1]
		if len(raw) != len("20060102150405") {
			continue
		}
		if !isDigits(raw) {
			continue
		}
		// 偶然14桁の数字が含まれる jobID を誤ってタイムスタンプ扱いしないよう、
		// 日付として妥当か検証し、不正な場合は次の候補へ進む。
		if _, err := time.ParseInLocation("20060102150405", raw, time.UTC); err != nil {
			continue
		}
		return raw
	}
	return ""
}

func isDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}
