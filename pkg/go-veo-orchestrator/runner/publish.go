package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// VideoPublisherRunner は Music Recipe とカット情報を動画向けメタデータとして出力します。
type VideoPublisherRunner struct {
	writer remoteio.Writer
}

// NewVideoPublisherRunner は VideoPublisherRunner を初期化します。
func NewVideoPublisherRunner(writer remoteio.Writer) *VideoPublisherRunner {
	return &VideoPublisherRunner{writer: writer}
}

// Run は HTML/Markdown ではなく、結合・検証に使う JSON メタデータだけを保存します。
func (pr *VideoPublisherRunner) Run(ctx context.Context, recipe *ports.VideoRecipe, outputDir string) (*ports.PublishResult, error) {
	if recipe == nil {
		return nil, fmt.Errorf("VideoRecipe が nil です")
	}
	if pr.writer == nil {
		return nil, fmt.Errorf("writer is required")
	}

	recipe.Normalize()
	metadata, err := pr.BuildMetadata(recipe)
	if err != nil {
		return nil, err
	}

	metadataPath, err := resolveOutputPath(outputDir, defaultVideoMetaJSON)
	if err != nil {
		return nil, fmt.Errorf("メタデータ出力パスの解決に失敗しました: %w", err)
	}

	if err := pr.writer.Write(ctx, metadataPath, bytes.NewReader(metadata),
		remoteio.WithContentType("application/json"),
		remoteio.WithCacheControl(defaultCacheControl),
	); err != nil {
		return nil, fmt.Errorf("動画メタデータの保存に失敗しました: %w", err)
	}

	imagePaths := make([]string, 0, len(recipe.Cuts))
	for _, cut := range recipe.Cuts {
		if cut.KeyframeReference == "" {
			continue
		}
		imagePaths = append(imagePaths, path.Join(defaultImageDir, path.Base(cut.KeyframeReference)))
	}

	return &ports.PublishResult{
		MetadataPath: metadataPath,
		ImagePaths:   imagePaths,
	}, nil
}

// BuildMetadata は VideoRecipe を整形済み JSON に変換します。
func (pr *VideoPublisherRunner) BuildMetadata(recipe *ports.VideoRecipe) ([]byte, error) {
	if recipe == nil {
		return nil, fmt.Errorf("VideoRecipe が nil です")
	}
	recipe.Normalize()
	data, err := json.MarshalIndent(recipe, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("動画メタデータのJSON変換に失敗しました: %w", err)
	}
	return data, nil
}
