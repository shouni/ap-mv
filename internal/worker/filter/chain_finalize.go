package filter

import (
	"context"
	"fmt"
	"strings"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/ports"
)

// ChainFinalizeFilter は、全カット生成後に継続チェーンをハードカットで1本の完成動画へ
// 結合するパイプラインステップです。videoGenの直後・PublishingFilterの直前で実行され、
// 動画生成を伴わないコマンド（画像のみ生成するパス）には含まれません。
type ChainFinalizeFilter struct {
	VideoProcessor ports.VideoProcessor
}

// Name returns the receiver name.
func (ChainFinalizeFilter) Name() string { return "chain_finalize" }

// Execute runs the receiver processing step.
func (f ChainFinalizeFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil || fc.VideoRecipe == nil {
		return fmt.Errorf("chain finalize requires video recipe")
	}
	if f.VideoProcessor == nil {
		return nil
	}
	chainEndURLs := chainEndVideoURLs(fc.VideoRecipe.Cuts)
	if len(chainEndURLs) == 0 {
		return nil
	}
	destURI := finalVideoDestURI(fc.OutputPath)
	if destURI == "" {
		return nil
	}
	finalURL, err := f.VideoProcessor.ConcatHardCut(ctx, chainEndURLs, destURI)
	if err != nil {
		return fmt.Errorf("concat chains: %w", err)
	}
	fc.VideoRecipe.FinalVideoURL = finalURL
	return nil
}

// chainEndVideoURLs は各継続チェーンの最終カットのVideoURLを、チェーンの登場順に返します。
// 「次のカットがチェーンの新規起点(IsChainStart)、または自身が最終カット」の位置を
// チェーン境界として判定します（3_video_gen.goのIsChainStartマーキングと対）。
func chainEndVideoURLs(cuts []orchestrator.Cut) []string {
	var urls []string
	for i, cut := range cuts {
		isBoundary := i == len(cuts)-1 || cuts[i+1].IsChainStart
		if isBoundary && strings.TrimSpace(cut.VideoURL) != "" {
			urls = append(urls, cut.VideoURL)
		}
	}
	return urls
}

// finalVideoDestURI は結合済み完成動画の保存先URIを組み立てます。
func finalVideoDestURI(outputPath string) string {
	outputPath = strings.TrimRight(strings.TrimSpace(outputPath), "/")
	if outputPath == "" {
		return ""
	}
	return outputPath + "/videos/final.mp4"
}
