package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/ports"
)

const defaultCacheControl = "public, max-age=1800"

// CutKeyframeRunner は、動画レシピを元に並列キーフレーム生成を管理します。
type CutKeyframeRunner struct {
	generator ports.CutImageGenerator
	writer    remoteio.Writer
}

// NewCutKeyframeRunner は、依存関係を注入して初期化します。
func NewCutKeyframeRunner(
	generator ports.CutImageGenerator,
	writer remoteio.Writer,
) *CutKeyframeRunner {
	return &CutKeyframeRunner{
		generator: generator,
		writer:    writer,
	}
}

// Run は、動画レシピを受け取り、カットのキーフレーム画像を生成します。
func (r *CutKeyframeRunner) Run(ctx context.Context, recipe *ports.VideoRecipe) ([]*imagePorts.ImageResponse, error) {
	if recipe == nil {
		return nil, fmt.Errorf("VideoRecipe がありません")
	}
	recipe.Normalize()

	slog.Info("Starting parallel cut keyframe generation")

	images, err := r.generator.Execute(ctx, recipe.Cuts)
	if err != nil {
		return nil, fmt.Errorf("cut keyframe generation failed: %w", err)
	}

	slog.Info("Successfully generated cut keyframes", "count", len(images))
	return images, nil
}

// RunAndSave はカットキーフレームを生成し、インデックスを付けて指定のパスに保存します。
func (r *CutKeyframeRunner) RunAndSave(ctx context.Context, recipe *ports.VideoRecipe, outputPath string) (*ports.VideoRecipe, error) {
	if recipe == nil {
		return nil, fmt.Errorf("VideoRecipe がありません")
	}
	recipe.Normalize()

	targetDir := resolveBaseURL(outputPath)
	basePath, err := resolveOutputPath(targetDir, defaultKeyframePath())
	if err != nil {
		return nil, fmt.Errorf("出力パスの解決に失敗しました: %w", err)
	}

	images, err := r.Run(ctx, recipe)
	if err != nil {
		return nil, err
	}

	if len(images) != len(recipe.Cuts) {
		return nil, fmt.Errorf("生成された画像の数(%d)とカット数(%d)が一致しません", len(images), len(recipe.Cuts))
	}
	for i, image := range images {
		keyframePath, err := generateIndexedPath(basePath, i+1)
		if err != nil {
			return nil, fmt.Errorf("cut %d のキーフレーム出力パス生成に失敗しました: %w", i+1, err)
		}

		slog.InfoContext(ctx, "キーフレーム画像を保存しています",
			"index", i+1,
			"path", keyframePath,
		)

		if err := r.writer.Write(ctx, keyframePath, bytes.NewReader(image.Data),
			remoteio.WithContentType(image.MimeType),
			remoteio.WithCacheControl(defaultCacheControl),
		); err != nil {
			return nil, fmt.Errorf("cut %d のキーフレーム保存に失敗しました (path: %s): %w", i+1, keyframePath, err)
		}
		recipe.Cuts[i].KeyframeReference = keyframePath
	}

	metadataPath, err := resolveOutputPath(targetDir, defaultVideoMetaJSON)
	if err != nil {
		return nil, fmt.Errorf("動画メタデータ出力パスの解決に失敗しました: %w", err)
	}

	plotData, err := json.MarshalIndent(recipe, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("動画レシピのJSON変換に失敗しました: %w", err)
	}

	slog.InfoContext(ctx, "更新された動画メタデータを保存しています", "path", metadataPath)
	if err := r.writer.Write(ctx, metadataPath, bytes.NewReader(plotData),
		remoteio.WithContentType("application/json"),
		remoteio.WithCacheControl(defaultCacheControl),
	); err != nil {
		return nil, fmt.Errorf("動画メタデータの保存に失敗しました: %w", err)
	}

	return recipe, nil
}
