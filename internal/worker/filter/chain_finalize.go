package filter

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/ports"
)

// ChainFinalizeFilter は、全カット生成後に継続チェーンをハードカットで1本の完成動画へ
// 結合するパイプラインステップです。videoGenの直後・PublishingFilterの直前で実行され、
// 動画生成を伴わないコマンド（画像のみ生成するパス）には含まれません。
type ChainFinalizeFilter struct {
	VideoProcessor ports.VideoProcessor
	// UsePreviousVideo は VideoGenerationFilter.UsePreviousVideo と一致させます。
	// false のときはカットが video-to-video で繋がらず、IsChainStart も立たないため、
	// 結合対象の選び方が変わります（chainEndVideoURLs 参照）。
	UsePreviousVideo bool
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
	chainEndURLs := chainEndVideoURLs(fc.VideoRecipe.Cuts, f.UsePreviousVideo)
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
	f.inspectFinalVideo(ctx, fc.VideoRecipe, finalURL)
	return nil
}

// finalDurationToleranceSeconds は、台本の総尺と完成動画の実尺として許すズレです。
//
// Veo が返すクリップの尺はリクエストどおりぴったりにはならず、結合時の再エンコードでも
// 端数が動きます。数秒のズレは正常なので、明らかな取りこぼし（カットの欠落や
// 生成失敗による短縮）だけを拾える幅にしています。
const finalDurationToleranceSeconds = 5.0

// inspectFinalVideo は完成動画を実測し、台本と食い違っていれば警告を残します。
//
// ここで失敗にはしません。動画自体は生成できており、再生もできる状態なので、
// 破棄の判断は運用側に委ねます。無言で通すと、尺が足りない、音が入っていないといった
// 破綻に気付けるのが「完成品を再生したとき」だけになります。
func (f ChainFinalizeFilter) inspectFinalVideo(ctx context.Context, recipe *video.Recipe, finalURL string) {
	stats, err := f.VideoProcessor.Probe(ctx, finalURL)
	if err != nil {
		slog.WarnContext(ctx, "完成動画の解析に失敗しました", "url", finalURL, "err", err)
		return
	}

	slog.InfoContext(ctx, "完成動画を解析しました",
		"url", finalURL, "duration_sec", stats.DurationSeconds, "has_audio", stats.HasAudio)

	if !stats.HasAudio {
		slog.WarnContext(ctx, "完成動画に音声トラックがありません", "url", finalURL)
	}
	if expected := expectedDurationSeconds(recipe); expected > 0 {
		if diff := math.Abs(stats.DurationSeconds - expected); diff > finalDurationToleranceSeconds {
			slog.WarnContext(ctx, "完成動画の尺が台本と一致しません",
				"url", finalURL, "expected_sec", expected, "actual_sec", stats.DurationSeconds, "diff_sec", diff)
		}
	}
}

// expectedDurationSeconds は台本が想定する総尺を返します。
// 最終カットの EndSec は Normalize が各カットの尺から積み上げた値です。
func expectedDurationSeconds(recipe *video.Recipe) float64 {
	if recipe == nil || len(recipe.Cuts) == 0 {
		return 0
	}
	return recipe.Cuts[len(recipe.Cuts)-1].EndSec
}

// chainEndVideoURLs は結合すべき動画のURLを、カットの登場順に返します。
//
// usePreviousVideo が true のとき、カットは video-to-video で数珠つなぎに生成され、
// チェーンの最終カットの動画がそのチェーン全体を含みます。そのため結合対象は各チェーンの
// 最終カットだけで、「次のカットがチェーンの新規起点(IsChainStart)、または自身が最終カット」
// の位置をチェーン境界として判定します（video_gen.goのIsChainStartマーキングと対）。
//
// usePreviousVideo が false のときはチェーンが存在せず、各カットが自身のキーフレームから
// 独立に生成された8秒前後のクリップです。IsChainStart を立てる経路も usePreviousVideo の
// 内側にあるため誰にも印が付きません。この判定を共有すると境界が最終カットだけになり、
// 完成動画が末尾1カットぶんに縮みます。ここでは全カットが結合対象です。
func chainEndVideoURLs(cuts []video.Cut, usePreviousVideo bool) []string {
	var urls []string
	for i, cut := range cuts {
		isBoundary := !usePreviousVideo || i == len(cuts)-1 || cuts[i+1].IsChainStart
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
