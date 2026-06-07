package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"

	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-veo-orchestrator/ports"
)

const (
	// maxInputSize は読み込みを許可する最大テキストサイズ (5MB) です。
	maxInputSize = 5 * 1024 * 1024
	// maxErrorResponseLength はエラーログに含める応答抜粋の最大文字数です。
	maxErrorResponseLength = 200
)

// jsonBlockRegex は、Markdown 形式の JSON ブロックを抽出するための正規表現です。
var jsonBlockRegex = regexp.MustCompile("(?s)```(?:json)?\\s*(.*\\S)\\s*```")

type VideoScriptRunner struct {
	promptBuilder ports.ScriptPrompt
	aiClient      gemini.ContentGenerator
	reader        ports.ContentReader
	aiModel       string
}

// NewVideoScriptRunner は依存関係を注入して初期化します。
func NewVideoScriptRunner(
	pb ports.ScriptPrompt,
	ai gemini.ContentGenerator,
	r ports.ContentReader,
	aiModel string,
) *VideoScriptRunner {
	return &VideoScriptRunner{
		promptBuilder: pb,
		aiClient:      ai,
		reader:        r,
		aiModel:       aiModel,
	}
}

// Run は Web ページまたは GCS から内容を抽出し、Gemini を用いて動画台本 JSON を生成します。
func (r *VideoScriptRunner) Run(ctx context.Context, sourceURL string, mode string) (*ports.VideoRecipe, error) {
	slog.Info("ScriptRunner: 処理を開始", "url", sourceURL)

	// 1. ソースからテキストを取得
	inputText, err := r.readContent(ctx, sourceURL)
	if err != nil {
		return nil, err
	}

	// 2. プロンプトの構築
	data := ports.TemplateData{InputText: inputText}
	finalPrompt, err := r.promptBuilder.Build(mode, &data)
	if err != nil {
		return nil, fmt.Errorf("プロンプトの構築に失敗しました: %w", err)
	}

	// 3. Gemini API を呼び出し
	slog.Info("ScriptRunner: Gemini APIを呼び出し中", "model", r.aiModel)
	resp, err := r.aiClient.GenerateContent(ctx, r.aiModel, finalPrompt)
	if err != nil {
		return nil, fmt.Errorf("Geminiによるコンテンツ生成に失敗しました: %w", err)
	}

	// 4. AI の応答をパース
	recipe, err := r.parseResponse(resp.Text)
	if err != nil {
		return nil, err
	}

	return recipe, nil
}

// readContent は、指定されたソースURLからコンテンツを取得します。
func (r *VideoScriptRunner) readContent(ctx context.Context, url string) (string, error) {
	rc, err := r.reader.Open(ctx, url)
	if err != nil {
		return "", fmt.Errorf("failed to read source: %w", err)
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			slog.WarnContext(ctx, "ストリームのクローズに失敗しました", "error", closeErr)
		}
	}()
	limitedReader := io.LimitReader(rc, int64(maxInputSize))
	content, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("読み込みに失敗しました: %w", err)
	}

	// 追加の読み込みを試みて切り捨てを判定
	oneMoreByte := make([]byte, 1)
	n, readErr := rc.Read(oneMoreByte)
	if readErr != nil && readErr != io.EOF {
		return "", fmt.Errorf("サイズ確認中にエラーが発生しました: %w", readErr)
	}

	if n > 0 {
		slog.WarnContext(ctx, "制限サイズに達したため切り捨てられました",
			"url", url,
			"limit_bytes", maxInputSize)
	}

	return strings.ToValidUTF8(string(content), ""), nil
}

// parseResponse は AI の応答から JSON を抽出し、構造体に変換します。
func (r *VideoScriptRunner) parseResponse(raw string) (*ports.VideoRecipe, error) {
	jsonStr := extractJSONString(raw)
	if jsonStr == "" {
		slog.Warn("AIの応答からJSONを抽出できませんでした。応答全体を対象にパースを試みます。",
			"response_snippet", truncateString(raw, 100))
		jsonStr = raw
	}

	var recipe ports.VideoRecipe
	if err := json.Unmarshal([]byte(jsonStr), &recipe); err != nil {
		return nil, fmt.Errorf("AI応答JSONの解析に失敗しました (抜粋: %q): %w",
			truncateString(raw, maxErrorResponseLength), err)
	}
	recipe.Normalize()

	return &recipe, nil
}

// extractJSONString は文字列から JSON 部分を抽出します。
func extractJSONString(raw string) string {
	cleanRaw := strings.TrimSpace(raw)

	if matches := jsonBlockRegex.FindStringSubmatch(cleanRaw); len(matches) > 1 {
		return matches[1]
	}

	first := firstJSONDelimiter(cleanRaw)
	last := lastJSONDelimiter(cleanRaw)
	if first != -1 && last != -1 && last > first {
		return cleanRaw[first : last+1]
	}

	return ""
}

func firstJSONDelimiter(s string) int {
	firstObj := strings.Index(s, "{")
	firstArr := strings.Index(s, "[")
	if firstObj == -1 || (firstArr != -1 && firstArr < firstObj) {
		return firstArr
	}
	return firstObj
}

func lastJSONDelimiter(s string) int {
	lastObj := strings.LastIndex(s, "}")
	lastArr := strings.LastIndex(s, "]")
	if lastObj == -1 || (lastArr != -1 && lastArr > lastObj) {
		return lastArr
	}
	return lastObj
}

// truncateString は指定された長さで文字列を安全に切り捨てます。
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
