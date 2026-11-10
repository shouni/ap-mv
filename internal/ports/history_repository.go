// Package ports defines the interfaces (ports) ap-mv's components depend on.
package ports

import (
	"context"
	"io"

	"github.com/shouni/ap-mv/internal/domain"
)

// KeyframeSink is the callback invoked for each file when keyframes are downloaded.
// name is the file name in the download, and r is a stream reading the file's contents.
type KeyframeSink func(name string, r io.Reader) error

// HistoryRepository loads and mutates generated MV history.
type HistoryRepository interface {
	// ListHistoryPage lists MV jobs. An empty stage means every stage; when given, only jobs at
	// that stage are returned (script-only jobs are domain.StageScript).
	ListHistoryPage(ctx context.Context, page int, perPage int, stage domain.JobStage) (domain.VideoHistoryPage, error)
	// GetHistory returns history formatted for display. **Signed URLs are left empty.**
	// The page follows same-origin paths and the handler signs exactly one at redirect time.
	// Prepaying one per cut would mean dozens of IAM SignBlob round trips for one detail page.
	GetHistory(ctx context.Context, jobID string) (domain.VideoHistoryDetail, error)
	// SignHistoryURLs fills in the signed URLs GetHistory left empty.
	// JSON callers (the MCP gateway) receive the URLs themselves without following redirects, so
	// only this path needs signing.
	SignHistoryURLs(ctx context.Context, detail *domain.VideoHistoryDetail) error
	// SignedObjectURL signs a gs:// URI taken from history already loaded.
	// It is for redirect handlers only, and what is passed is always a URI recorded in history,
	// so the caller's input is never signed as-is.
	SignedObjectURL(ctx context.Context, uri string) (string, error)
	// GetRecipe returns the stored VideoRecipe as-is. It is the read for the round trip of editing
	// and writing back with SaveRecipe, a separate path from GetHistory, which formats for display.
	GetRecipe(ctx context.Context, jobID string) (*domain.VideoRecipe, error)
	// SaveRecipe overwrites the job's VideoRecipe. It is the path that lets the cut list be fixed
	// before generation (script only) at zero image cost.
	SaveRecipe(ctx context.Context, jobID string, recipe *domain.VideoRecipe) error
	DeleteHistory(ctx context.Context, jobID string) error
	DownloadKeyframes(ctx context.Context, jobID string, sink KeyframeSink) error
	KeyframeZipSignedURL(ctx context.Context, jobID string) (string, error)
	// GetVeoUsage loads the job's recorded Veo generation tally. It returns (nil, nil) for jobs
	// that predate the record or never reached video generation — a missing record is a normal
	// state, not an error, and callers fall back to the recipe-derived estimate.
	GetVeoUsage(ctx context.Context, jobID string) (*domain.VeoUsage, error)
}
