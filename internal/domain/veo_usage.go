package domain

import (
	"sort"
	"strings"
	"time"
)

// VeoUsageFileName is the name of the Veo generation usage file placed in the job's output directory.
// It is a separate file from video_music_meta.json: the metadata's structure is decided by
// go-veo-orchestrator's VideoRecipe, and adding usage there would need a release of that library.
// Their purposes and lifetimes differ too (one describes the finished piece, the other records
// billing), so keeping them apart is clearer for readers as well.
const VeoUsageFileName = "veo_usage.json"

// VeoUsageSchemaVersion is the schema version of veo_usage.json. It is filled from the start so
// readers can tell future format changes apart.
const VeoUsageSchemaVersion = 1

// VeoCutUsage is the usage of the Veo generations that actually ran for one cut.
// Calls of 2 or more means the cut was re-rendered (Cloud Tasks redelivery, section
// regeneration and so on).
type VeoCutUsage struct {
	CutIndex int `json:"cut_index"`
	// Calls is the number of successful generations. Failed calls are not counted (they are not normally billed).
	Calls int `json:"calls"`
	// SubmittedSeconds is the total duration of successful generations. Rendering the same cut twice adds its duration twice.
	SubmittedSeconds float64 `json:"submitted_seconds"`
	// LastGeneratedAt is the time of the last successful generation.
	LastGeneratedAt time.Time `json:"last_generated_at,omitzero"`
}

// VeoUsage is the usage of the generations actually sent to Veo for one job.
//
// Unlike "the finished length" computable from the VideoRecipe, this accumulates the calls
// themselves. The difference between the two is what regeneration threw away.
//
// It is not an exact ledger. It does a read-modify-write after every cut, so if the same job runs
// twice at once updates can be lost (undercounting the usage in that case). Not stopping the
// generation itself takes priority, so a failed record is logged and the run moves on.
type VeoUsage struct {
	SchemaVersion int    `json:"schema_version"`
	JobID         string `json:"job_id,omitempty"`
	// Model is the Veo model used for generation. It is empty when the task did not name a model,
	// and readers substitute the default model at display time.
	Model string `json:"model,omitempty"`
	// Calls is the total number of successful generations.
	Calls int `json:"calls"`
	// SubmittedSeconds is the grand total duration of successful generations (= an estimate of the actual billable seconds).
	SubmittedSeconds float64       `json:"submitted_seconds"`
	Cuts             []VeoCutUsage `json:"cuts,omitempty"`
	UpdatedAt        time.Time     `json:"updated_at,omitzero"`
}

// Record adds one successful generation to the usage.
// It overwrites the model only when model is non-empty, so even when tasks with and without a
// model mix, a known value is never wiped out by an empty one.
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

// CutCalls returns the number of successful generations for the given cut. It returns 0 when there is no record.
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
