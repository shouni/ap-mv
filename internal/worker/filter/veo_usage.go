package filter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

// recordVeoUsage は、成功した1カットぶんの生成実績を veo_usage.json へ加算します。
//
// 完成品の尺（VideoRecipe から算出できる）と違い、実際に Veo へ投げた回数は生成した瞬間に
// しか分かりません。Cloud Tasks の再配信で同じカットを焼き直すと、完成品は変わらないまま
// 課金だけが増えるため、その差を見えるようにするのがこの記録の目的です。
//
// 記録の失敗は返しません。会計のために生成を止めるのは本末転倒だからです（呼び出し側は
// 既に Veo へ課金済みで、ここで失敗を返すと Cloud Tasks が同じカットを焼き直しにきます）。
// 失敗はログに残し、実績が過小になることを受け入れます。
func recordVeoUsage(ctx context.Context, fc *Context, cut *video.Cut) {
	if fc == nil || cut == nil || fc.Writer == nil {
		return
	}
	uri := veoUsageURI(fc.OutputPath)
	if uri == "" {
		return
	}

	usage := loadVeoUsage(ctx, fc, uri)
	if fc.Task != nil {
		usage.JobID = fc.Task.JobID
	}
	usage.Record(cut.CutIndex, cut.DurationSec, taskVeoModel(fc.Task), time.Now().UTC())

	if err := writeVeoUsage(ctx, fc.Writer, uri, usage); err != nil {
		slog.WarnContext(ctx, "failed to record veo usage; generation continues",
			"uri", uri,
			"cut_index", cut.CutIndex,
			"error", err,
		)
	}
}

// veoUsageURI builds the usage file URI under a job's output directory.
func veoUsageURI(outputPath string) string {
	outputPath = strings.TrimRight(strings.TrimSpace(outputPath), "/")
	if outputPath == "" {
		return ""
	}
	return outputPath + "/" + domain.VeoUsageFileName
}

// taskVeoModel returns the Veo model the task pinned, or "" when it left the choice to the
// configured default. Recording "" rather than guessing keeps the reader honest: it falls back
// to the model configured at display time and says so.
func taskVeoModel(task *domain.Task) string {
	if task == nil {
		return ""
	}
	return strings.TrimSpace(task.VeoModel)
}

// loadVeoUsage reads the existing record, returning an empty one for the first cut of a job
// (or whenever the file can't be read — a broken record must not stop generation, so this
// starts a fresh count rather than failing).
func loadVeoUsage(ctx context.Context, fc *Context, uri string) *domain.VeoUsage {
	usage := &domain.VeoUsage{SchemaVersion: domain.VeoUsageSchemaVersion}
	if fc.Reader == nil {
		return usage
	}
	rc, err := fc.Reader.Open(ctx, uri)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.WarnContext(ctx, "failed to read veo usage; starting a fresh record", "uri", uri, "error", err)
		}
		return usage
	}
	defer func() { _ = rc.Close() }()

	raw, err := io.ReadAll(rc)
	if err != nil {
		slog.WarnContext(ctx, "failed to read veo usage; starting a fresh record", "uri", uri, "error", err)
		return usage
	}
	if err := json.Unmarshal(raw, usage); err != nil {
		slog.WarnContext(ctx, "failed to parse veo usage; starting a fresh record", "uri", uri, "error", err)
		return &domain.VeoUsage{SchemaVersion: domain.VeoUsageSchemaVersion}
	}
	return usage
}

// writeVeoUsage persists the record.
func writeVeoUsage(ctx context.Context, writer remoteio.OutputWriter, uri string, usage *domain.VeoUsage) error {
	raw, err := json.MarshalIndent(usage, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal veo usage: %w", err)
	}
	if err := writer.Write(ctx, uri, bytes.NewReader(raw), remoteio.WithContentType("application/json")); err != nil {
		return fmt.Errorf("write veo usage: %w", err)
	}
	return nil
}
