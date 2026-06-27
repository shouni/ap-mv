package filter

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"

	"ap-mv/internal/domain"
)

type ZipUploadFilter struct{}

func (ZipUploadFilter) Name() string { return "zip_upload" }

// Execute builds a keyframe zip and uploads it to GCS at {outputPath}keyframes.zip.
// For regenerate tasks, only runs when OverwriteKeyframe is true.
func (ZipUploadFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil || fc.VideoRecipe == nil || fc.Writer == nil || fc.Reader == nil {
		return nil
	}
	if fc.Task != nil && fc.Task.Command == domain.CommandRegenerateCutKeyframe && !fc.Task.OverwriteKeyframe {
		return nil
	}

	outputPath := fc.OutputPath
	if fc.Task != nil && fc.Task.Command == domain.CommandRegenerateCutKeyframe {
		outputPath = originalJobOutputPath(fc.Task.RecipeURL)
		if outputPath == "" {
			return fmt.Errorf("zip_upload: cannot resolve original job output path from RecipeURL %q", fc.Task.RecipeURL)
		}
	}
	if outputPath == "" {
		return nil
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for _, cut := range fc.VideoRecipe.Cuts {
		ref := strings.TrimSpace(cut.KeyframeReference)
		if ref == "" {
			continue
		}
		ext := path.Ext(ref)
		if ext == "" {
			ext = ".png"
		}
		name := fmt.Sprintf("cut_%02d%s", cut.CutIndex, ext)
		if err := writeZipEntry(ctx, zw, name, ref, fc.Reader); err != nil {
			return err
		}
	}

	if txt := buildInputsTxt(fc.VideoRecipe.Cuts); txt != "" {
		fw, err := zw.Create("inputs.txt")
		if err != nil {
			return fmt.Errorf("zip_upload: create inputs.txt: %w", err)
		}
		if _, err := io.WriteString(fw, txt); err != nil {
			return fmt.Errorf("zip_upload: write inputs.txt: %w", err)
		}
	}

	historyCuts := orchestratorCutsToHistoryCuts(fc.VideoRecipe.Cuts)
	if ass := domain.GenerateASS(historyCuts); ass != "" {
		fw, err := zw.Create("subtitles.ass")
		if err != nil {
			return fmt.Errorf("zip_upload: create subtitles.ass: %w", err)
		}
		if _, err := io.WriteString(fw, ass); err != nil {
			return fmt.Errorf("zip_upload: write subtitles.ass: %w", err)
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("zip_upload: finalize zip: %w", err)
	}

	zipURI := strings.TrimRight(outputPath, "/") + "/keyframes.zip"
	return fc.Writer.Write(ctx, zipURI, &buf, remoteio.WithContentType("application/zip"))
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
