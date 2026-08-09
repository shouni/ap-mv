package filter

import (
	"context"
	"errors"
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
	// ExpandCutsToSupportedDurations はカットの所属セクションを cut.SectionIndex から直接
	// 判定するため、（呼び出し元がまだ Normalize していない可能性に備えて）ここで確実に
	// 補完しておく。Normalize は冪等なので、既に呼ばれていても無害。
	fc.VideoRecipe.Normalize()
	// Veo がサポートしない尺（reference_to_videoなら8秒以外、image_to_videoなら4/6/8秒以外）の
	// カットは生成前に分割・丸めする。生成済みカットは実動画の尺と metadata がずれないよう変更
	// しない。usePreviousVideo が true の場合、video_extension の累積尺がVeoの上限
	// (orchestrator.VeoContinuationMaxDurationSec) に達する手前で自動的にチェーンをリセットする
	// （詳細は orchestrator.ExpandCutsToSupportedDurations のコメント参照）。
	fc.VideoRecipe.Cuts = orchestrator.ExpandCutsToSupportedDurations(fc.VideoRecipe.Cuts, f.UsePreviousVideo, fc.Characters, referenceImagesSupported(f.resolvedVideoRunner(fc)))
	// 実行方式の優先順位: (1) VideoRunner が設定されていれば直接実行（1カットずつ生成し、
	// 残りがあれば継続タスクをenqueueして中断する resumable な方式）を最優先する。
	// (2) VideoRunner がなく orchestrator workflow があれば、そちらに全カットの生成を委譲する
	// （resumable ではなく、内部で全カットをまとめて処理する）。Workflows.Video は
	// go-veo-orchestrator v1.7.0 以降、VideoRunner 未設定でも nil にならず常に非nilの
	// runner（未設定時は ErrVideoRunnerNotConfigured を返すダミー実装）が入るため、
	// nilチェックでは判定できない。実際に呼び出してエラーを見る。
	// (3) orchestrator workflow 自体がなければ runDirect を呼び、runner未設定のエラーを返す。
	if f.hasVideoRunner(fc) {
		return f.runDirect(ctx, fc)
	}
	if fc.Workflows != nil {
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
	return ports.RunnerCapabilities(runner).ReferenceImages
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
		if errors.Is(err, orchestrator.ErrVideoRunnerNotConfigured) {
			return fmt.Errorf("video generation requires a VideoRunner (neither a direct runner nor an orchestrator workflow VideoRunner is configured): %w", err)
		}
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
		// ExpandCutsToSupportedDurations は、video_extension の累積尺がVeoの上限に達する
		// 手前でチェーンをリセットし、そのカットを7秒固定ではなく{4,6,8}秒のいずれかに
		// 揃える。7秒固定でない = チェーンリセット後の新規ベースカットなので、
		// PreviousVideoURI を引き継がずに生成する。
		if f.UsePreviousVideo && cut.DurationSec != orchestrator.VeoVideoExtensionDurationSec {
			lastVideoID = ""
			cut.IsChainStart = true
			// i > 0 は「ジョブ内で最初のチェーンではない」= 直前に実際に生成された
			// チェーンが存在することを意味する。その最終フレームを次チェーンの
			// 参照画像として引き継ぎ、静的な立ち絵からの独立生成による見た目の
			// ブレ（衣装ズレ等）を抑える。ただし IsSectionStart は「意図的な場面転換」
			// （実際の曲のセクション境界、または scene_split.go によるシーン内リセット
			// のどちらか。詳細は orchestrator.ExpandCutsToSupportedDurations の
			// コメント参照）なので、どちらの理由でも直前の絵を引き継がず、そのカット
			// 自身に割り当てられたキーフレーム参照のまま生成する。
			if i > 0 && !cut.IsSectionStart {
				if err := f.applyChainResetKeyframe(ctx, fc, cut, fc.VideoRecipe.Cuts[i-1].VideoURL); err != nil {
					return err
				}
			}
		}
		if err := f.generateCut(ctx, runner, fc, cut, lastVideoID, orchestrator.Cuts(fc.VideoRecipe.Cuts).NextLastFrameReference(i)); err != nil {
			return err
		}
		if f.UsePreviousVideo && cut.DurationSec == orchestrator.VeoVideoExtensionDurationSec {
			if err := f.colorCorrectExtensionCut(ctx, fc, cut); err != nil {
				return err
			}
		}
		lastVideoID = cut.VideoID
		// 継続タスクのエンキューに失敗した場合、このジョブはここで止まる。キューは
		// max-attempts=1 で運用しており、失敗したタスクは再配信されないため、残りの
		// カットを生成する担い手がいなくなる（成果物が出てこないことで気付く）。
		// 生成済みカットの状態は保存済みなので、同じレシピを投げ直せば
		// Cut.IsGenerated() のカットはスキップされ、途中から再開できる。
		//
		// 検証中は、一時的な失敗が再試行に隠れて systematic なバグを見落とすことと、
		// 失敗したカットの再生成が Veo の課金をそのまま倍にすることを避けるため、
		// この「止まる」挙動を選んでいる。定常運用へ移すときに max-attempts を
		// 増やせば、ここは自動で再開されるようになる。
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

// videoSeed は Veo 動画生成へ渡すシードを解決します。カットのキャラクターに紐づくシード
// （キーフレーム画像生成と同じ値、go-veo-orchestrator@v1.6.0/keyframe/generator.go が
// 常に char.Seed のみを使うのと揃えています）を返します。シード無し（0 = Veoリクエストから
// 省略）の独立生成はチェーン起点ごとに見た目が確率的にブレるため、少なくともキャラクター
// 単位で固定したシードを常に渡すことで、同一ジョブ内・ジョブ間のキャラ一貫性を高めます。
// キャラクターにシードが無い場合のみ 0 を返します（従来挙動）。
func videoSeed(fc *Context, cut *orchestrator.Cut) int64 {
	if fc.Characters != nil {
		if char := fc.Characters.GetCharacter(strings.TrimSpace(cut.CharacterID)); char != nil && char.Seed != nil {
			return *char.Seed
		}
	}
	return 0
}

// generateCut runs a single cut through the video runner and updates its status, VideoID, and
// VideoURL in place. lastVideoID chains the previous cut's video as this cut's PreviousVideoURI
// context (video-to-video continuation). lastFrameRef is the next cut's keyframe used as this
// cut's ending frame (frames_to_video interpolation). The request is built first and then
// classified via ports.ClassifyVeoRequest — the same decision the adapter makes when building
// the Veo body — so the prompt guidance always matches how Veo actually interprets the request.
func (f VideoGenerationFilter) generateCut(ctx context.Context, runner ports.VideoRunner, fc *Context, cut *orchestrator.Cut, lastVideoID, lastFrameRef string) error {
	req := ports.VideoGenerationRequest{
		CutIndex:           cut.CutIndex,
		DurationSec:        cut.DurationSec,
		Seed:               videoSeed(fc, cut),
		PreviousVideoURI:   lastVideoID,
		ImageReference:     cut.KeyframeReference,
		LastFrameReference: lastFrameRef,
		ReferenceImages:    orchestrator.CutReferenceImages(*cut, fc.Characters),
		AudioReference:     cut.AudioReference,
	}
	mode := ports.ClassifyVeoRequest(req, f.UsePreviousVideo, ports.RunnerCapabilities(runner))
	// lastFrame として実際に使われない参照はリクエストに残さない（ログ・再現時の
	// リクエスト内容を adapter が送る内容と一致させる）。
	if mode != ports.VeoModeFramesToVideo {
		req.LastFrameReference = ""
	}
	req.Prompt = videoPrompt(*cut, mode)
	res, err := runner.Run(ctx, req)
	if err != nil {
		return fmt.Errorf("generate cut %d: %w", cut.CutIndex, err)
	}
	cut.Status = orchestrator.CutStatusGenerated
	cut.VideoID = res.VideoID
	cut.VideoURL = res.CloudURL
	// 課金が発生した直後に記録する。完成品の尺（レシピから常に算出できる）と違い、
	// 「何回投げたか」はこの瞬間にしか分からず、再配信で焼き直された分はここでしか数えられない。
	recordVeoUsage(ctx, fc, cut)
	return nil
}

// enqueueContinuation persists the in-progress VideoRecipe and enqueues a
// CommandVideoGenContinuation task to resume generation of the remaining pending cuts, then
// returns ErrPipelineDeferred so the pipeline stops here instead of treating this run as complete.
//
// The continuation task is enqueued under a deterministic name derived from job ID + cutIndex
// (see continuationTaskID), so that if Cloud Tasks redelivers the current task (at-least-once
// delivery) and this function runs again for the same completed cut, the second enqueue attempt
// is rejected by Cloud Tasks as a duplicate instead of creating a second continuation task. This
// does not by itself prevent the redelivered execution from re-generating cutIndex's video (that
// would require checking persisted, authoritative state before generating each cut) — it only
// keeps a redelivery from fanning out into duplicate downstream tasks.
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
	if err := fc.TaskQueue.EnqueueWithName(ctx, continuationTaskID(fc.Task.JobID, cutIndex), &nextTask); err != nil {
		return fmt.Errorf("enqueue continuation after cut %d: %w", cutIndex, err)
	}
	return ErrPipelineDeferred
}

// continuationTaskID builds a deterministic Cloud Tasks task ID for the continuation enqueued
// after cutIndex finishes generating. Cloud Tasks task IDs must match [A-Za-z0-9_-]{1,500}, so
// any other character in jobID is replaced with "_" defensively.
func continuationTaskID(jobID string, cutIndex int) string {
	safeJobID := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, jobID)
	return fmt.Sprintf("%s-cont-cut-%d", safeJobID, cutIndex)
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
