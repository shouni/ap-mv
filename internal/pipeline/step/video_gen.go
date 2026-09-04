package step

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/veo"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
	"github.com/shouni/ap-mv/internal/ports"
)

// VideoGenerationStep は、VideoRecipeから実際の動画を生成するパイプラインステップです。
type VideoGenerationStep struct {
	Runner ports.VideoRunner
	// UsePreviousVideo は VEO_USE_PREVIOUS_VIDEO 設定を反映します。
	// true の場合、先頭カット以降は video_extension 用の尺（7秒固定）へ正規化します。
	UsePreviousVideo bool
	// VideoProcessor は、チェーンリセット時に直前チェーンの最終フレームを抽出するために
	// 使います。nilの場合はフレーム抽出をスキップし、静的な立ち絵参照画像のまま生成します。
	VideoProcessor ports.VideoProcessor
	// SectionScoped が true のとき、生成対象を sc.Task.SectionIndex のセクションに属する
	// カットだけに絞ります（section_video コマンド）。**レシピからは何も取り除きません**。
	// 対象外のカットを削るのではなく飛ばすだけなのは、後続の Publishing が保存するのが
	// この同じレシピで、削った状態を保存すれば他セクションのカットがそのまま消えるためです。
	// 継続タスクはレシピをペイロードで持ち回る（enqueueContinuation 参照）ので、一度削れた
	// レシピは最後まで削れたまま運ばれます。
	SectionScoped bool
}

// Name returns the receiver name.
func (VideoGenerationStep) Name() string { return "video_gen" }

// Execute runs the receiver processing step.
func (f VideoGenerationStep) Execute(ctx context.Context, sc *Context) error {
	if sc == nil || sc.Task == nil {
		return fmt.Errorf("video generation requires task and recipe")
	}
	ctx = videoOutputContext(ctx, sc)
	if err := ensureVideoRecipe(sc); err != nil {
		return err
	}
	applyTaskAudioURLToVideoRecipe(sc.Task, sc.VideoRecipe)
	// ExpandCutsToSupportedDurations はカットの所属セクションを cut.SectionIndex から直接
	// 判定するため、（呼び出し元がまだ Normalize していない可能性に備えて）ここで確実に
	// 補完しておく。Normalize は冪等なので、既に呼ばれていても無害。
	sc.VideoRecipe.Normalize()
	// Veo がサポートしない尺（reference_to_videoなら8秒以外、image_to_videoなら4/6/8秒以外）の
	// カットは生成前に分割・丸めする。生成済みカットは実動画の尺と metadata がずれないよう変更
	// しない。usePreviousVideo が true の場合、video_extension の累積尺がVeoの上限
	// (veo.ContinuationMaxDurationSec) に達する手前で自動的にチェーンをリセットする
	// （詳細は veo.ExpandCutsToSupportedDurations のコメント参照）。
	sc.VideoRecipe.Cuts = veo.ExpandCutsToSupportedDurations(sc.VideoRecipe.Cuts, f.UsePreviousVideo, sc.Characters, referenceImagesSupported(f.resolvedVideoRunner(sc)))
	// 実行方式の優先順位: (1) VideoRunner が設定されていれば直接実行（1カットずつ生成し、
	// 残りがあれば継続タスクをenqueueして中断する resumable な方式）を最優先する。
	// (2) VideoRunner がなく orchestrator workflow があれば、そちらに全カットの生成を委譲する
	// （resumable ではなく、内部で全カットをまとめて処理する）。Workflows.Video は
	// go-veo-orchestrator v1.7.0 以降、VideoRunner 未設定でも nil にならず常に非nilの
	// runner（未設定時は ErrVideoRunnerNotConfigured を返すダミー実装）が入るため、
	// nilチェックでは判定できない。実際に呼び出してエラーを見る。
	// (3) orchestrator workflow 自体がなければ runDirect を呼び、runner未設定のエラーを返す。
	if f.hasVideoRunner(sc) {
		return f.runDirect(ctx, sc)
	}
	if sc.Workflows != nil {
		return f.runWithWorkflow(ctx, sc)
	}
	return f.runDirect(ctx, sc)
}

// resolvedVideoRunner returns the VideoRunner that will actually be used for generation:
// f.Runner takes priority, falling back to sc.VideoRunner.
func (f VideoGenerationStep) resolvedVideoRunner(sc *Context) ports.VideoRunner {
	if f.Runner != nil {
		return f.Runner
	}
	return sc.VideoRunner
}

// referenceImagesSupported は、実際に使われる VideoRunner が Veo の referenceImages
// （reference_to_video、8秒固定）に対応しているかを返します。Runner が
// ports.ReferenceImagesSupporter を実装していない場合（テストダブル等）は false を返し、
// image_to_video 用の {4,6,8} 秒での丸めにフォールバックします。
func referenceImagesSupported(runner ports.VideoRunner) bool {
	return ports.RunnerCapabilities(runner).ReferenceImages
}

func (f VideoGenerationStep) hasVideoRunner(sc *Context) bool {
	if f.Runner != nil {
		return true
	}
	return sc != nil && sc.VideoRunner != nil
}

// runWithWorkflow delegates cut generation to the orchestrator workflow.
// The workflow handles all cuts internally, so deferred continuation is not required.
func (f VideoGenerationStep) runWithWorkflow(ctx context.Context, sc *Context) error {
	if _, err := sc.Workflows.Video.Run(ctx, sc.VideoRecipe); err != nil {
		if errors.Is(err, orchestrator.ErrVideoRunnerNotConfigured) {
			return fmt.Errorf("video generation requires a VideoRunner (neither a direct runner nor an orchestrator workflow VideoRunner is configured): %w", err)
		}
		return err
	}
	recipe, err := toDomainRecipe(sc.VideoRecipe)
	sc.Recipe = recipe
	return err
}

// runDirect generates cuts one by one via VideoRunner.
// After each cut it enqueues a continuation task and defers when cuts remain,
// allowing Cloud Tasks to stay within its execution time limit.
func (f VideoGenerationStep) runDirect(ctx context.Context, sc *Context) error {
	applyTaskCharacterIDToVideoRecipe(sc.Task, sc.VideoRecipe)
	sc.VideoRecipe.Normalize()
	runner := f.resolvedVideoRunner(sc)
	if runner == nil {
		return fmt.Errorf("video runner is not configured")
	}

	inScope, err := f.cutScope(sc)
	if err != nil {
		return err
	}

	lastVideoID := ""
	for i := range sc.VideoRecipe.Cuts {
		cut := &sc.VideoRecipe.Cuts[i]
		if cut.IsGenerated() {
			if cut.VideoID != "" {
				lastVideoID = cut.VideoID
			}
			continue
		}
		if !inScope(cut.SectionIndex) {
			// 対象外セクションの未生成カット。ここには動画が存在しないので、次に生成する
			// カットはこの穴を跨いで前の動画へ繋ぐことができない。チェーンの引き継ぎ元を
			// 空へ戻し、穴の向こう側を新しいチェーンの起点として生成させる。
			lastVideoID = ""
			continue
		}
		// ExpandCutsToSupportedDurations は、video_extension の累積尺がVeoの上限に達する
		// 手前でチェーンをリセットし、そのカットを7秒固定ではなく{4,6,8}秒のいずれかに
		// 揃える。7秒固定でない = チェーンリセット後の新規ベースカットなので、
		// PreviousVideoURI を引き継がずに生成する。
		if f.UsePreviousVideo && cut.DurationSec != veo.VideoExtensionDurationSec {
			lastVideoID = ""
			cut.IsChainStart = true
			// i > 0 は「ジョブ内で最初のチェーンではない」= 直前に実際に生成された
			// チェーンが存在することを意味する。その最終フレームを次チェーンの
			// 参照画像として引き継ぎ、静的な立ち絵からの独立生成による見た目の
			// ブレ（衣装ズレ等）を抑える。ただし IsSectionStart は「意図的な場面転換」
			// （実際の曲のセクション境界、または scene_split.go によるシーン内リセット
			// のどちらか。詳細は veo.ExpandCutsToSupportedDurations の
			// コメント参照）なので、どちらの理由でも直前の絵を引き継がず、そのカット
			// 自身に割り当てられたキーフレーム参照のまま生成する。
			if i > 0 && !cut.IsSectionStart {
				if err := f.applyChainResetKeyframe(ctx, sc, cut, sc.VideoRecipe.Cuts[i-1].VideoURL); err != nil {
					return err
				}
			}
		}
		// 引き継ぐ動画が無い状態で生成するカットは、それ自体が新しいチェーンの起点です。
		// ここで印を付けないと chain_finalize が境界を見落とし、直前のチェーンの最終動画を
		// 結合対象から落とします（＝完成動画からそのぶんが丸ごと消えます）。尺の条件で
		// リセットされた場合は上で既に立っているので、ここは冪等な念押しです。
		if f.UsePreviousVideo && lastVideoID == "" {
			cut.IsChainStart = true
		}
		if err := f.generateCut(ctx, runner, sc, cut, lastVideoID, video.Cuts(sc.VideoRecipe.Cuts).NextLastFrameReference(i)); err != nil {
			return err
		}
		if f.UsePreviousVideo && cut.DurationSec == veo.VideoExtensionDurationSec {
			if err := f.colorCorrectExtensionCut(ctx, sc, cut); err != nil {
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
		if hasPendingCuts(sc.VideoRecipe, inScope) && sc.TaskQueue != nil {
			return enqueueContinuation(ctx, sc, cut.CutIndex)
		}
	}
	domainRecipe, err := toDomainRecipe(sc.VideoRecipe)
	if err != nil {
		return err
	}
	sc.Recipe = domainRecipe
	return nil
}

// videoSeed は Veo 動画生成へ渡すシードを解決します。カットのキャラクターに紐づくシード
// （キーフレーム画像生成と同じ値、go-veo-orchestrator@v1.6.0/keyframe/generator.go が
// 常に char.Seed のみを使うのと揃えています）を返します。シード無し（0 = Veoリクエストから
// 省略）の独立生成はチェーン起点ごとに見た目が確率的にブレるため、少なくともキャラクター
// 単位で固定したシードを常に渡すことで、同一ジョブ内・ジョブ間のキャラ一貫性を高めます。
// キャラクターにシードが無い場合のみ 0 を返します（従来挙動）。
func videoSeed(sc *Context, cut *video.Cut) int64 {
	if sc.Characters != nil {
		if char := sc.Characters.GetCharacter(strings.TrimSpace(cut.CharacterID)); char != nil && char.Seed != nil {
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
func (f VideoGenerationStep) generateCut(ctx context.Context, runner ports.VideoRunner, sc *Context, cut *video.Cut, lastVideoID, lastFrameRef string) error {
	req := ports.VideoGenerationRequest{
		CutIndex:           cut.CutIndex,
		DurationSec:        cut.DurationSec,
		Seed:               videoSeed(sc, cut),
		PreviousVideoURI:   lastVideoID,
		ImageReference:     cut.KeyframeReference,
		LastFrameReference: lastFrameRef,
		ReferenceImages:    video.CutReferenceImages(*cut, sc.Characters),
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
	cut.Status = video.CutStatusGenerated
	cut.VideoID = res.VideoID
	cut.VideoURL = res.CloudURL
	// 課金が発生した直後に記録する。完成品の尺（レシピから常に算出できる）と違い、
	// 「何回投げたか」はこの瞬間にしか分からず、再配信で焼き直された分はここでしか数えられない。
	recordVeoUsage(ctx, sc, cut)
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
func enqueueContinuation(ctx context.Context, sc *Context, cutIndex int) error {
	domainRecipe, err := toDomainRecipe(sc.VideoRecipe)
	if err != nil {
		return err
	}
	sc.Recipe = domainRecipe
	nextTask := *sc.Task
	// Command を上書きする前に元のコマンドを控える。継続側の実行計画は「結合するか否か」を
	// これで決めるため、失うと section_video の続きが結合まで走ってしまう。
	// 既に継続タスクなら、その継続が持っている OriginCommand をそのまま引き継ぐ。
	if sc.Task.Command != domain.CommandVideoGenContinuation {
		nextTask.OriginCommand = sc.Task.Command
	}
	nextTask.Command = domain.CommandVideoGenContinuation
	nextTask.Recipe = sc.Recipe
	nextTask.VideoRecipe = sc.VideoRecipe
	nextTask.CreatedAt = time.Now().UTC()
	if err := sc.TaskQueue.EnqueueWithName(ctx, continuationTaskID(sc.Task.JobID, cutIndex), &nextTask); err != nil {
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

// ensureVideoRecipe converts sc.Recipe to sc.VideoRecipe when it is not already set.
func ensureVideoRecipe(sc *Context) error {
	if sc.VideoRecipe != nil {
		return nil
	}
	if sc.Recipe == nil {
		return fmt.Errorf("video generation requires recipe")
	}
	recipe, err := toVideoRecipe(sc.Recipe)
	if err != nil {
		return err
	}
	sc.VideoRecipe = recipe
	return nil
}

// videoOutputContext adds the output base URI to the context when available.
func videoOutputContext(ctx context.Context, sc *Context) context.Context {
	if sc == nil {
		return ctx
	}
	return ports.WithVideoOutputBaseURI(ctx, sc.OutputPath)
}

// hasPendingCuts reports whether a recipe has cuts awaiting video generation.
//
// inScope は今回の実行が担当するカットの判定です（nil は全カット担当）。ここで範囲を見ないと、
// セクション実行が担当外の未生成カットを「残っている」と数えて継続タスクを撒き、その継続が
// 何も生成せず終わる、という空振りが毎回1本ぶん増えます。
func hasPendingCuts(recipe *video.Recipe, inScope func(sectionIndex int) bool) bool {
	if recipe == nil {
		return false
	}
	for i := range recipe.Cuts {
		if recipe.Cuts[i].IsGenerated() {
			continue
		}
		if inScope == nil || inScope(recipe.Cuts[i].SectionIndex) {
			return true
		}
	}
	return false
}

// cutScope は、この実行が動画を生成すべきカットの判定関数を返します。
// SectionScoped でないときは常に true を返す関数（＝全カット担当）です。
func (f VideoGenerationStep) cutScope(sc *Context) (func(sectionIndex int) bool, error) {
	if !f.SectionScoped {
		return func(int) bool { return true }, nil
	}
	if sc.Task.SectionIndex == nil {
		return nil, fmt.Errorf("section-scoped video generation requires section_index")
	}
	sectionIndex := *sc.Task.SectionIndex
	sections := sc.VideoRecipe.MusicRecipe.Sections
	if sectionIndex < 0 || sectionIndex >= len(sections) {
		return nil, fmt.Errorf("section_index %d is out of range (recipe has %d sections)", sectionIndex, len(sections))
	}
	// cut.SectionIndex は1始まりなので、0始まりの sectionIndex と比較する際は +1 する
	// （SectionSelectStep / resolveRegenTargets と同じ規則）。
	wantSectionIndex := sectionIndex + 1

	found := false
	for i := range sc.VideoRecipe.Cuts {
		if sc.VideoRecipe.Cuts[i].SectionIndex == wantSectionIndex {
			found = true
			break
		}
	}
	// 該当カットが1つも無ければ、生成も保存もせずに正常終了してしまう。押した本人には
	// 「動いたのに何も起きない」としか見えないので、ここで落とす。
	if !found {
		return nil, fmt.Errorf("no cuts found in section %d (%s)", sectionIndex, sections[sectionIndex].Name)
	}
	return func(cutSectionIndex int) bool { return cutSectionIndex == wantSectionIndex }, nil
}
