package runner

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/keyframe"
)

const (
	// プロンプト構成用の定数
	designPromptBaseTemplate = "Masterpiece character design sheet of %s"
	designLayoutDefault      = "multiple views (front, side, back), standing full body"
	designLayoutPromptFormat = "Layout: %s, side-by-side, separate character charts"
)

// fileNameSanitizer はファイル名として使用できない文字を置換します。
var fileNameSanitizer = strings.NewReplacer(
	"/", "_",
	`\`, "_",
	":", "_",
	"*", "_",
	"?", "_",
	`"`, "_",
	"<", "_",
	">", "_",
	"|", "_",
)

type DesignImageGenerator interface {
	GenerateFusedImage(ctx context.Context, req imagePorts.ImageFusionRequest) (*imagePorts.ImageResponse, error)
}

// DesignRunner はキャラクターデザインシート生成を実行するランナーです。
type DesignRunner struct {
	composer    *keyframe.VideoComposer
	generator   DesignImageGenerator
	writer      remoteio.Writer
	model       string
	styleSuffix string
}

// NewDesignRunner は依存関係を注入して初期化します。
func NewDesignRunner(composer *keyframe.VideoComposer, generator DesignImageGenerator, writer remoteio.Writer, model, styleSuffix string) *DesignRunner {
	return &DesignRunner{
		composer:    composer,
		generator:   generator,
		writer:      writer,
		model:       model,
		styleSuffix: styleSuffix,
	}
}

// Run は、指定されたキャラクターIDのデザインシートを生成し、指定されたディレクトリに保存します。
func (dr *DesignRunner) Run(ctx context.Context, charIDs []string, seed int64, outputDir string) (string, int64, error) {
	// 1. 複数キャラの情報を集約
	imageURIs, descriptions, err := dr.collectCharacterURIs(charIDs)
	if err != nil {
		return "", 0, fmt.Errorf("キャラクター資産の収集に失敗しました: %w", err)
	}

	slog.Info("Executing design work generation",
		slog.Any("chars", charIDs),
		slog.Int("ref_count", len(imageURIs)),
	)

	// 2. プロンプト構築
	designPrompt := dr.buildDesignPrompt(descriptions)
	if designPrompt == "" {
		return "", 0, fmt.Errorf("キャラクター情報が空のため、プロンプトを生成できませんでした")
	}

	// 3. 生成リクエスト
	fusionReq := imagePorts.ImageFusionRequest{
		GenerationOptions: imagePorts.GenerationOptions{
			Model:       dr.model,
			Prompt:      designPrompt,
			AspectRatio: keyframe.DesignAspectRatio,
			ImageSize:   keyframe.ImageSize2K,
			Seed:        ptrInt64(seed),
		},
		Images: imageURIs,
	}

	// 4. 生成実行
	resp, err := dr.generator.GenerateFusedImage(ctx, fusionReq)
	if err != nil {
		slog.Error("Design generation failed", "error", err)
		return "", 0, fmt.Errorf("画像の生成に失敗しました: %w", err)
	}

	// 5. 画像の保存
	outputPath, err := dr.saveResponseImage(ctx, *resp, charIDs, outputDir)
	if err != nil {
		slog.Error("Failed to save image", "error", err)
		return "", 0, fmt.Errorf("画像の保存に失敗しました: %w", err)
	}

	return outputPath, resp.UsedSeed, nil
}

// saveResponseImage は、生成された画像データを指定されたディレクトリに保存します。
func (dr *DesignRunner) saveResponseImage(ctx context.Context, resp imagePorts.ImageResponse, charIDs []string, outputDir string) (string, error) {
	charTags := strings.Join(charIDs, "_")
	sanitizedCharTags := fileNameSanitizer.Replace(charTags)

	extension := getPreferredExtension(resp.MimeType)
	relativePath := path.Join(characterDesignDir, fmt.Sprintf("design_%s%s", sanitizedCharTags, extension))
	finalPath, err := resolveOutputPath(outputDir, relativePath)
	if err != nil {
		return "", fmt.Errorf("画像保存パスの生成に失敗しました (baseDir: %s, relativePath: %s): %w", outputDir, relativePath, err)
	}

	if err = dr.writer.Write(ctx, finalPath, bytes.NewReader(resp.Data),
		remoteio.WithContentType(resp.MimeType),
		remoteio.WithCacheControl(defaultCacheControl),
	); err != nil {
		return "", fmt.Errorf("画像の保存に失敗しました (path: %s): %w", finalPath, err)
	}

	return finalPath, nil
}

// buildDesignPrompt はキャラクターデザインシート生成用の詳細なプロンプト文字列を構築します。
func (dr *DesignRunner) buildDesignPrompt(descriptions []string) string {
	numChars := len(descriptions)
	if numChars == 0 {
		slog.Warn("buildDesignPrompt called with empty descriptions")
		return ""
	}

	var subjects string
	if numChars > 1 {
		subjectParts := make([]string, numChars)
		for i, d := range descriptions {
			subjectParts[i] = fmt.Sprintf("[Subject %d: %s]", i+1, d)
		}
		subjects = fmt.Sprintf("%d DIFFERENT characters: %s", numChars, strings.Join(subjectParts, " "))
	} else {
		subjects = descriptions[0]
	}

	base := fmt.Sprintf(designPromptBaseTemplate, subjects)
	layoutPrompt := fmt.Sprintf(designLayoutPromptFormat, designLayoutDefault)

	promptParts := []string{base, layoutPrompt}
	if dr.styleSuffix != "" {
		promptParts = append(promptParts, dr.styleSuffix)
	}
	promptParts = append(promptParts, "white background", "sharp focus", "2k resolution")

	return strings.Join(promptParts, ", ")
}

// collectCharacterURIs はキャラクター情報を収集し、ImageURIスライスと説明文を返します。
func (dr *DesignRunner) collectCharacterURIs(ids []string) ([]imagePorts.ImageURI, []string, error) {
	var uris []imagePorts.ImageURI
	var descriptions []string
	var missingIDs []string
	processedIDs := make(map[string]struct{})

	for _, id := range ids {
		if _, exists := processedIDs[id]; exists {
			continue
		}
		processedIDs[id] = struct{}{}

		char := dr.composer.Characters.GetCharacter(id)
		if char == nil {
			missingIDs = append(missingIDs, id)
			continue
		}

		// File API URI があれば取得
		fileURI := dr.composer.GetCharacterResourceURI(char.ID)

		if char.ReferenceURL == "" && fileURI == "" {
			slog.Warn("キャラクターに有効な参照画像がないためスキップします", "id", id)
			continue
		}

		uris = append(uris, imagePorts.ImageURI{
			ReferenceURL: char.ReferenceURL,
			FileAPIURI:   fileURI,
		})

		desc := char.Name
		if len(char.VisualCues) > 0 {
			desc = fmt.Sprintf("%s (%s)", char.Name, strings.Join(char.VisualCues, ", "))
		}
		descriptions = append(descriptions, desc)
	}

	if len(missingIDs) > 0 {
		return nil, nil, fmt.Errorf("一部のキャラクターIDが見つかりませんでした: %s", strings.Join(missingIDs, ", "))
	}

	if len(uris) == 0 {
		return nil, nil, fmt.Errorf("有効な参照画像を持つキャラクターが1つも見つかりませんでした (対象ID: %s)", strings.Join(ids, ", "))
	}

	return uris, descriptions, nil
}

func ptrInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func getPreferredExtension(mimeType string) string {
	preferred := map[string]string{"image/png": ".png", "image/jpeg": ".jpg"}
	if ext, ok := preferred[mimeType]; ok {
		return ext
	}
	return ".png"
}
