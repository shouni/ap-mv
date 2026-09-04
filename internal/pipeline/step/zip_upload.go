package step

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/ap-mv/internal/domain"
)

// ZipUploadStep は、生成済みキーフレームをZIPにまとめてアップロードするパイプラインステップです。
type ZipUploadStep struct{}

// Name returns the receiver name.
func (ZipUploadStep) Name() string { return "zip_upload" }

// isKeyframeRegenCommand reports whether the command regenerates keyframes of an existing job
// (a single cut or a whole section). Both write their result back into the original job, so the
// zip step treats them identically.
func isKeyframeRegenCommand(command domain.TaskCommand) bool {
	return command == domain.CommandRegenerateCutKeyframe || command == domain.CommandRegenerateSectionKeyframes
}

// Execute builds a keyframe zip and streams it to GCS at {outputPath}keyframes.zip.
// For regenerate tasks, only runs when OverwriteKeyframe is true.
func (ZipUploadStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil || sc.VideoRecipe == nil || sc.Writer == nil || sc.Reader == nil {
		return nil
	}
	if sc.Task != nil && isKeyframeRegenCommand(sc.Task.Command) && !sc.Task.OverwriteKeyframe {
		return nil
	}

	outputPath := sc.OutputPath
	if sc.Task != nil && (isKeyframeRegenCommand(sc.Task.Command) || sc.Task.Command == domain.CommandRegenerateZip) {
		outputPath = originalJobOutputPath(sc.Task.RecipeURL)
		if outputPath == "" {
			return fmt.Errorf("zip_upload: cannot resolve original job output path from RecipeURL %q", sc.Task.RecipeURL)
		}
	}
	if outputPath == "" {
		return nil
	}

	pr, pw := io.Pipe()

	var writeErr error
	go func() {
		zw := zip.NewWriter(pw)
		defer func() {
			if err := zw.Close(); writeErr == nil {
				writeErr = err
			}
			pw.CloseWithError(writeErr)
		}()

		for _, cut := range sc.VideoRecipe.Cuts {
			ref := strings.TrimSpace(cut.KeyframeReference)
			if ref == "" {
				continue
			}
			ext := path.Ext(ref)
			if ext == "" {
				ext = ".png"
			}
			name := fmt.Sprintf("cut_%02d%s", cut.CutIndex, ext)
			if err := writeZipEntry(ctx, zw, name, ref, sc.Reader); err != nil {
				writeErr = err
				return
			}
		}

		if txt := buildInputsTxt(sc.VideoRecipe.Cuts); txt != "" {
			fw, err := zw.Create("inputs.txt")
			if err != nil {
				writeErr = fmt.Errorf("zip_upload: create inputs.txt: %w", err)
				return
			}
			if _, err := io.WriteString(fw, txt); err != nil {
				writeErr = fmt.Errorf("zip_upload: write inputs.txt: %w", err)
				return
			}
		}

		applyLyricsToVideoRecipeCuts(sc.VideoRecipe)
		historyCuts := orchestratorCutsToHistoryCuts(sc.VideoRecipe.Cuts)
		if ass := domain.GenerateASS(historyCuts, sc.Task.ASSColors(), sc.VideoRecipe.MusicRecipe.Tempo); ass != "" {
			fw, err := zw.Create("subtitles.ass")
			if err != nil {
				writeErr = fmt.Errorf("zip_upload: create subtitles.ass: %w", err)
				return
			}
			if _, err := io.WriteString(fw, ass); err != nil {
				writeErr = fmt.Errorf("zip_upload: write subtitles.ass: %w", err)
				return
			}
		}
	}()

	zipURI := strings.TrimRight(outputPath, "/") + "/keyframes.zip"
	return sc.Writer.Write(ctx, zipURI, pr, remoteio.WithContentType("application/zip"))
}

func writeZipEntry(ctx context.Context, zw *zip.Writer, name string, uri string, reader interface {
	Open(context.Context, string) (io.ReadCloser, error)
}) error {
	rc, err := reader.Open(ctx, uri)
	if err != nil {
		return fmt.Errorf("zip_upload: open %s: %w", name, err)
	}
	defer rc.Close()
	fw, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("zip_upload: create zip entry %s: %w", name, err)
	}
	if _, err := io.Copy(fw, rc); err != nil {
		return fmt.Errorf("zip_upload: write zip entry %s: %w", name, err)
	}
	return nil
}
