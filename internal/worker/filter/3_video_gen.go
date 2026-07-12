package filter

import (
	"context"
	"fmt"
	"strings"
	"time"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// VideoGenerationFilter は、VideoRecipeから実際の動画を生成するパイプラインステップです。
type VideoGenerationFilter struct {
	Runner ports.VideoRunner
	// UsePreviousVideo は VEO_USE_PREVIOUS_VIDEO 設定を反映します。
	// true の場合、先頭カット以降は video_extension 用の尺（7秒固定）へ正規化します。
	UsePreviousVideo bool
	// VideoProcessor は、チェーンリセット時に直前チェーンの最終フレームを抽出するために
	// 使います。nilの場合はフレーム抽出をスキップし、静的な立ち絵参照画像のまま生成します。
	VideoProcessor ports.VideoProcessor
}

// Name returns the receiver name.
func (VideoGenerationFilter) Name() string { return "video_gen" }

// Execute runs the receiver processing step.
func (f VideoGenerationFilter) Execute(ctx context.Context, fc *Context) error {
	if fc == nil || fc.Task == nil {
		return fmt.Errorf("video generation requires task and recipe")
	}
	ctx = videoOutputContext(ctx, fc)
	if err := ensureVideoRecipe(fc); err != nil {
		return err
	}
	applyTaskAudioURLToVideoRecipe(fc.Task, fc.VideoRecipe)
	// Veo がサポートしない尺（reference_to_videoなら8秒以外、image_to_videoなら4/6/8秒以外）の
	// カットは生成前に分割・丸めする。生成済みカットは実動画の尺と metadata がずれないよう変更
	// しない。usePreviousVideo が true の場合、video_extension の累積尺がVeoの上限
	// (veoContinuationMaxDurationSec) に達する手前で自動的にチェーンをリセットする
	// （詳細は expandCutsToSupportedDurations のコメント参照）。
	fc.VideoRecipe.Cuts = expandCutsToSupportedDurations(fc.VideoRecipe.Cuts, f.UsePreviousVideo, fc.VideoRecipe.MusicRecipe.Sections, fc.Characters, referenceImagesSupported(f.resolvedVideoRunner(fc)))
	// 実行方式の優先順位: (1) VideoRunner が設定されていれば直接実行（1カットずつ生成し、
	// 残りがあれば継続タスクをenqueueして中断する resumable な方式）を最優先する。
	// (2) VideoRunner がなく orchestrator workflow があれば、そちらに全カットの生成を委譲する
	// （resumable ではなく、内部で全カットをまとめて処理する）。
	// (3) どちらもなければ runDirect を呼び、runner未設定のエラーを返す。
	if f.hasVideoRunner(fc) {
		return f.runDirect(ctx, fc)
	}
	if fc.Workflows != nil && fc.Workflows.Video != nil {
		return f.runWithWorkflow(ctx, fc)
	}
	return f.runDirect(ctx, fc)
}

// resolvedVideoRunner returns the VideoRunner that will actually be used for generation:
// f.Runner takes priority, falling back to fc.VideoRunner.
func (f VideoGenerationFilter) resolvedVideoRunner(fc *Context) ports.VideoRunner {
	if f.Runner != nil {
		return f.Runner
	}
	return fc.VideoRunner
}

// referenceImagesSupported は、実際に使われる VideoRunner が Veo の referenceImages
// （reference_to_video、8秒固定）に対応しているかを返します。Runner が
// ports.ReferenceImagesSupporter を実装していない場合（テストダブル等）は false を返し、
// image_to_video 用の {4,6,8} 秒での丸めにフォールバックします。
func referenceImagesSupported(runner ports.VideoRunner) bool {
	rs, ok := runner.(ports.ReferenceImagesSupporter)
	return ok && rs.SupportsReferenceImages()
}

func (f VideoGenerationFilter) hasVideoRunner(fc *Context) bool {
	if f.Runner != nil {
		return true
	}
	return fc != nil && fc.VideoRunner != nil
}

// runWithWorkflow delegates cut generation to the orchestrator workflow.
// The workflow handles all cuts internally, so deferred continuation is not required.
func (f VideoGenerationFilter) runWithWorkflow(ctx context.Context, fc *Context) error {
	if _, err := fc.Workflows.Video.Run(ctx, fc.VideoRecipe); err != nil {
		return err
	}
	recipe, err := toDomainRecipe(fc.VideoRecipe)
	fc.Recipe = recipe
	return err
}

// runDirect generates cuts one by one via VideoRunner.
// After each cut it enqueues a continuation task and defers when cuts remain,
// allowing Cloud Tasks to stay within its execution time limit.
func (f VideoGenerationFilter) runDirect(ctx context.Context, fc *Context) error {
	applyTaskCharacterIDToVideoRecipe(fc.Task, fc.VideoRecipe)
	fc.VideoRecipe.Normalize()
	runner := f.resolvedVideoRunner(fc)
	if runner == nil {
		return fmt.Errorf("video runner is not configured")
	}

	lastVideoID := ""
	for i := range fc.VideoRecipe.Cuts {
		cut := &fc.VideoRecipe.Cuts[i]
		if cut.IsGenerated() {
			if cut.VideoID != "" {
				lastVideoID = cut.VideoID
			}
			continue
		}
		// expandCutsToSupportedDurations は、video_extension の累積尺がVeoの上限に達する
		// 手前でチェーンをリセットし、そのカットを7秒固定ではなく{4,6,8}秒のいずれかに
		// 揃える。7秒固定でない = チェーンリセット後の新規ベースカットなので、
		// PreviousVideoIDを引き継がずに生成する。
		if f.UsePreviousVideo && cut.DurationSec != veoVideoExtensionDurationSec {
			lastVideoID = ""
			cut.IsChainStart = true
			// i > 0 は「ジョブ内で最初のチェーンではない」= 直前に実際に生成された
			// チェーンが存在することを意味する。その最終フレームを次チェーンの
			// 参照画像として引き継ぎ、静的な立ち絵からの独立生成による見た目の
			// ブレ（衣装ズレ等）を抑える。ただしセクション境界（IsSectionStart）は
			// 意図的な場面転換なので、直前セクションの絵を引き継がず、そのカット
			// 自身に割り当てられたキーフレーム参照のまま生成する。
			if i > 0 && !cut.IsSectionStart {
				if err := f.applyChainResetKeyframe(ctx, fc, cut, fc.VideoRecipe.Cuts[i-1].VideoURL); err != nil {
					return err
				}
			}
		}
		if err := generateCut(ctx, runner, fc, cut, lastVideoID); err != nil {
			return err
		}
		lastVideoID = cut.VideoID
		// 継続タスクのエンキューに失敗した場合、Cloud Tasks は元のタスクを再試行する。
		// 再試行時には直前の続きタスクのペイロード（このカットはまだ pending）から再開するため、
		// カットが再生成される可能性があるが、状態の整合性は保たれる。
		if hasPendingCuts(fc.VideoRecipe) && fc.TaskQueue != nil {
			return enqueueContinuation(ctx, fc, cut.CutIndex)
		}
	}
	domainRecipe, err := toDomainRecipe(fc.VideoRecipe)
	if err != nil {
		return err
	}
	fc.Recipe = domainRecipe
	return nil
}

// applyChainResetKeyframe は、チェーンリセット後の新規ベースカットの参照画像を、静的な
// 立ち絵ではなく直前チェーンの実際の生成結果の最終フレームへ差し替えます。VideoProcessor
// が未設定、または直前動画のURLが空の場合は何もしません（従来通り静的な立ち絵のまま生成）。
func (f VideoGenerationFilter) applyChainResetKeyframe(ctx context.Context, fc *Context, cut *orchestrator.Cut, previousVideoURL string) error {
	if f.VideoProcessor == nil || strings.TrimSpace(previousVideoURL) == "" {
		return nil
	}
	destURI := chainFrameDestURI(fc.OutputPath, cut.CutIndex)
	if destURI == "" {
		return nil
	}
	frameURI, err := f.VideoProcessor.ExtractLastFrame(ctx, previousVideoURL, destURI)
	if err != nil {
		return fmt.Errorf("extract last frame for chain reset at cut %d: %w", cut.CutIndex, err)
	}
	cut.KeyframeReference = frameURI
	return nil
}

// chainFrameDestURI はチェーンリセット時に抽出する最終フレーム画像の保存先URIを組み立てます。
func chainFrameDestURI(outputPath string, cutIndex int) string {
	outputPath = strings.TrimRight(strings.TrimSpace(outputPath), "/")
	if outputPath == "" {
		return ""
	}
	return fmt.Sprintf("%s/images/chain_frame_cut_%02d.png", outputPath, cutIndex)
}

// videoSeed は Veo 動画生成へ渡すシードを解決します。レシピ全体のシード
// (MusicRecipe.Seed) が明示されていればそれを最優先し、無ければカットの
// キャラクターに紐づくシード（キーフレーム画像生成と同じ値）へフォールバック
// します。シード無し（0 = Veoリクエストから省略）の独立生成はチェーン起点
// ごとに見た目が確率的にブレるため、少なくともキャラクター単位で固定した
// シードを常に渡すことで、同一ジョブ内・ジョブ間のキャラ一貫性を高めます。
// どちらも無い場合のみ 0 を返します（従来挙動）。
func videoSeed(fc *Context, cut *orchestrator.Cut) int64 {
	if seed := fc.VideoRecipe.MusicRecipe.Seed; seed != nil {
		return *seed
	}
	if fc.Characters != nil {
		if char := fc.Characters.GetCharacter(strings.TrimSpace(cut.CharacterID)); char != nil && char.Seed != nil {
			return *char.Seed
		}
	}
	return 0
}

// generateCut runs a single cut through the video runner and updates its status, VideoID, and
// VideoURL in place. lastVideoID chains the previous cut's video as this cut's PreviousVideoID
// context (video-to-video continuation).
func generateCut(ctx context.Context, runner ports.VideoRunner, fc *Context, cut *orchestrator.Cut, lastVideoID string) error {
	res, err := runner.Run(ctx, ports.VideoGenerationRequest{
		CutIndex:        cut.CutIndex,
		Prompt:          videoPrompt(*cut),
		DurationSec:     cut.DurationSec,
		Seed:            videoSeed(fc, cut),
		PreviousVideoID: lastVideoID,
		ImageReference:  cut.KeyframeReference,
		ReferenceImages: buildReferenceImages(fc, *cut),
		AudioReference:  cut.AudioReference,
	})
	if err != nil {
		return fmt.Errorf("generate cut %d: %w", cut.CutIndex, err)
	}
	cut.Status = orchestrator.CutStatusGenerated
	cut.VideoID = res.VideoID
	cut.VideoURL = res.CloudURL
	return nil
}

// enqueueContinuation persists the in-progress VideoRecipe and enqueues a
// CommandVideoGenContinuation task to resume generation of the remaining pending cuts, then
// returns ErrPipelineDeferred so the pipeline stops here instead of treating this run as complete.
func enqueueContinuation(ctx context.Context, fc *Context, cutIndex int) error {
	domainRecipe, err := toDomainRecipe(fc.VideoRecipe)
	if err != nil {
		return err
	}
	fc.Recipe = domainRecipe
	nextTask := *fc.Task
	nextTask.Command = domain.CommandVideoGenContinuation
	nextTask.Recipe = fc.Recipe
	nextTask.VideoRecipe = fc.VideoRecipe
	nextTask.CreatedAt = time.Now().UTC()
	if err := fc.TaskQueue.Enqueue(ctx, &nextTask); err != nil {
		return fmt.Errorf("enqueue continuation after cut %d: %w", cutIndex, err)
	}
	return ErrPipelineDeferred
}

// ensureVideoRecipe converts fc.Recipe to fc.VideoRecipe when it is not already set.
func ensureVideoRecipe(fc *Context) error {
	if fc.VideoRecipe != nil {
		return nil
	}
	if fc.Recipe == nil {
		return fmt.Errorf("video generation requires recipe")
	}
	recipe, err := toVideoRecipe(fc.Recipe)
	if err != nil {
		return err
	}
	fc.VideoRecipe = recipe
	return nil
}

// buildReferenceImages はキャラクター立ち絵とキーフレームから referenceImages 用 URI リストを組み立てます。
func buildReferenceImages(fc *Context, cut orchestrator.Cut) []string {
	var refs []string
	if fc.Characters != nil {
		if char := fc.Characters.GetCharacter(strings.TrimSpace(cut.CharacterID)); char != nil {
			if ref := strings.TrimSpace(char.ReferenceURL); ref != "" {
				refs = append(refs, ref)
			}
		}
	}
	if ref := strings.TrimSpace(cut.KeyframeReference); ref != "" {
		refs = append(refs, ref)
	}
	return refs
}

// videoOutputContext adds the output base URI to the context when available.
func videoOutputContext(ctx context.Context, fc *Context) context.Context {
	if fc == nil {
		return ctx
	}
	return ports.WithVideoOutputBaseURI(ctx, fc.OutputPath)
}

// hasPendingCuts reports whether a recipe has cuts awaiting video generation.
func hasPendingCuts(recipe *orchestrator.VideoRecipe) bool {
	if recipe == nil {
		return false
	}
	for i := range recipe.Cuts {
		if !recipe.Cuts[i].IsGenerated() {
			return true
		}
	}
	return false
}

// videoPrompt builds the prompt used for video generation.
func videoPrompt(cut orchestrator.Cut) string {
	anchor := strings.TrimSpace(cut.VisualAnchor)
	cue := strings.TrimSpace(cut.AudioCue)
	if cue == "" {
		return anchor
	}
	sync := "Synchronize motion and camera timing with audio cue: " + cue
	if anchor == "" {
		return sync
	}
	return anchor + "\n" + sync
}
