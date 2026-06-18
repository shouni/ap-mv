package repository

import (
	"strings"
	"time"
)

func formatHistoryCreatedAt(jobID string) string {
	const displayTimeLayout = "2006-01-02 15:04 MST"
	jst := time.FixedZone("JST", 9*60*60)

	parts := strings.Split(jobID, "-")
	for i := 0; i+1 < len(parts); i++ {
		raw := parts[i] + parts[i+1]
		if len(raw) != len("20060102150405") {
			continue
		}
		if !isDigits(raw) {
			continue
		}
		createdAt, err := time.ParseInLocation("20060102150405", raw, time.UTC)
		if err != nil {
			return ""
		}
		return createdAt.In(jst).Format(displayTimeLayout)
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
