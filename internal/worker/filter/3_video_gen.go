package filter

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/assets"
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
		if err := f.generateCut(ctx, runner, fc, cut, lastVideoID, nextCutLastFrameReference(fc.VideoRecipe.Cuts, i)); err != nil {
			return err
		}
		if f.UsePreviousVideo && cut.DurationSec == veoVideoExtensionDurationSec {
			if err := f.colorCorrectExtensionCut(ctx, fc, cut); err != nil {
				return err
			}
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

// colorCorrectExtensionCut は、video_extension（video-to-video継続）で生成された直後のカットの
// 彩度を、そのカットのシーン用キーフレーム画像（cut.KeyframeReference）へ引き戻します。
// Veo の video_extension は直前の生成結果を条件入力として再利用するため、継続を重ねるたびに
// 彩度がドリフトして蓄積します（実運用で確認済み: 継続1回目で彩度+20%、以降のラウンドでも
// コントラストが単調に増加し続けた）。補正後のVideoURL/VideoIDを次カットのPreviousVideoIDとして
// 使うことで、ドリフトが次世代へ複利的に蓄積するのを防ぎます。
//
// 補正の基準には char.ReferenceURL（キャラクター立ち絵）を使わないこと。あれは白背景に
// 3方向ターンアラウンドを並べたデザインシートで、背景の白が画像全体の平均彩度を大きく
// 引き下げてしまう（実測: SATAVG=5.2、実際のシーンキーフレームは28.6前後）。これを基準にすると
// 動画側が不自然に彩度不足（色褪せた見た目）になる回帰が実際に発生した。cut.KeyframeReference
// は「Cinematic anime style」で描かれた実際のシーン画像で、image_to_video/reference_to_video
// 生成時に使われたのと同じ色味の基準を持つため、こちらを使う。
//
// VideoProcessor未設定、またはカットにキーフレーム参照が無い場合は何もしません
// （従来通り無補正のまま）。
func (f VideoGenerationFilter) colorCorrectExtensionCut(ctx context.Context, fc *Context, cut *orchestrator.Cut) error {
	if f.VideoProcessor == nil {
		return nil
	}
	referenceURL := strings.TrimSpace(cut.KeyframeReference)
	if referenceURL == "" {
		return nil
	}
	destURI := colorCorrectedVideoDestURI(fc.OutputPath, cut.CutIndex)
	if destURI == "" {
		return nil
	}
	correctedURI, err := f.VideoProcessor.ColorMatchSaturation(ctx, cut.VideoURL, referenceURL, destURI)
	if err != nil {
		return fmt.Errorf("color match saturation for cut %d: %w", cut.CutIndex, err)
	}
	cut.VideoURL = correctedURI
	cut.VideoID = correctedURI
	return nil
}

// colorCorrectedVideoDestURI は彩度補正済み動画の保存先URIを組み立てます。
func colorCorrectedVideoDestURI(outputPath string, cutIndex int) string {
	outputPath = strings.TrimRight(strings.TrimSpace(outputPath), "/")
	if outputPath == "" {
		return ""
	}
	return fmt.Sprintf("%s/videos/cut_%02d_color_corrected.mp4", outputPath, cutIndex)
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
// VideoURL in place. lastVideoID chains the previous cut's video as this cut's PreviousVideoID
// context (video-to-video continuation). lastFrameRef is the next cut's keyframe used as this
// cut's ending frame (frames_to_video interpolation); it is only sent when the request actually
// resolves to the image input path (mode == frames_to_video), keeping the request consistent
// with how the adapter builds the Veo body.
func (f VideoGenerationFilter) generateCut(ctx context.Context, runner ports.VideoRunner, fc *Context, cut *orchestrator.Cut, lastVideoID, lastFrameRef string) error {
	referenceImages := buildReferenceImages(fc, *cut)
	mode := videoGenModeFor(f.UsePreviousVideo, lastVideoID, referenceImages, lastFrameRef, runner)
	if mode != veoModeFramesToVideo {
		lastFrameRef = ""
	}
	res, err := runner.Run(ctx, ports.VideoGenerationRequest{
		CutIndex:           cut.CutIndex,
		Prompt:             videoPrompt(*cut, mode),
		DurationSec:        cut.DurationSec,
		Seed:               videoSeed(fc, cut),
		PreviousVideoID:    lastVideoID,
		ImageReference:     cut.KeyframeReference,
		LastFrameReference: lastFrameRef,
		ReferenceImages:    referenceImages,
		AudioReference:     cut.AudioReference,
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

// veoGenerationMode は、Veo へのリクエストが実際にどの生成機能で解釈されるかを表します。
// 値は assets/prompts/video_gen/ 配下のプロンプトファイル名（拡張子なし）と一致します。
type veoGenerationMode string

const (
	// veoModeImageToVideo はキーフレーム画像を image 入力とする image_to_video です。
	veoModeImageToVideo veoGenerationMode = "image_to_video"
	// veoModeFramesToVideo はキーフレーム画像を image 入力、次カットのキーフレームを
	// lastFrame 入力とする first/last frame 補間です（Veo 2 / Veo 3.1 系のみ、Fast も対応）。
	veoModeFramesToVideo veoGenerationMode = "frames_to_video"
	// veoModeReferenceToVideo は [キャラ立ち絵, キーフレーム] を referenceImages とする
	// reference_to_video です（Veo 3系の非Fastモデルのみ、8秒固定）。
	veoModeReferenceToVideo veoGenerationMode = "reference_to_video"
	// veoModeVideoExtension は前カット動画を video 入力とする video_extension
	// （video-to-video継続）です。このモードでは画像参照は一切送られません。
	veoModeVideoExtension veoGenerationMode = "video_extension"
)

// videoGenGuidance は Veo 生成モード別のプロンプトガイダンスを保持します。
// 埋め込みアセット（コンパイル時に存在が保証される）のため実行時の読み込み失敗は
// 事実上起こらないが、万一欠けてもガイダンス無しで生成を続行する（テストで全モードの
// 存在を検証しているため、欠落はCIで検出される）。
var videoGenGuidance = sync.OnceValue(func() map[string]string {
	prompts, err := assets.LoadVideoGenPrompts()
	if err != nil {
		return map[string]string{}
	}
	return prompts
})

// videoGenModeFor は、このカットのリクエストが Veo のどの生成機能で解釈されるかを判定します。
// 分岐は adapters.VertexVeoRunner.buildGenerateBody と揃えています:
// video 入力があれば画像参照は送られず video_extension、referenceImages が使えるなら
// reference_to_video、image 入力になるカットで lastFrame（次カットのキーフレーム）が使えるなら
// frames_to_video、それ以外は image_to_video。runner が ports.ReferenceImagesSupporter /
// ports.LastFrameSupporter を実装しない場合（テストダブル等）は image_to_video 側へ倒します。
func videoGenModeFor(usePreviousVideo bool, lastVideoID string, referenceImages []string, lastFrameRef string, runner ports.VideoRunner) veoGenerationMode {
	// adapters.previousVideoMedia は gs:// スキームの PreviousVideoID のみ video 入力にする。
	if usePreviousVideo && strings.HasPrefix(strings.TrimSpace(lastVideoID), "gs://") {
		return veoModeVideoExtension
	}
	if len(referenceImages) > 0 && referenceImagesSupported(runner) {
		return veoModeReferenceToVideo
	}
	if strings.TrimSpace(lastFrameRef) != "" && lastFrameSupported(runner) {
		return veoModeFramesToVideo
	}
	return veoModeImageToVideo
}

// lastFrameSupported は、実際に使われる VideoRunner が Veo の lastFrame
// （first/last frame 補間）に対応しているかを返します。Runner が ports.LastFrameSupporter を
// 実装していない場合（テストダブル等）は false を返し、従来どおり image_to_video で生成します。
func lastFrameSupported(runner ports.VideoRunner) bool {
	ls, ok := runner.(ports.LastFrameSupporter)
	return ok && ls.SupportsLastFrame()
}

// nextCutLastFrameReference は、cuts[i] の終了フレーム（frames_to_video 補間の lastFrame）
// として使う「次カットのキーフレーム参照」を返します。次カットの開始フレームでこのカットを
// 終えることで、カット境界の繋ぎ（構図・キャラの見た目）を滑らかにします。以下の場合は
// 空文字を返し、従来どおり終了フレーム指定なしで生成します:
//   - 次カットが無い、または次カットにキーフレームが無い
//   - 次カットがセクション境界（意図的な場面転換なので、現カットの絵を次セクションの構図へ
//     寄せない。applyChainResetKeyframe が境界で絵を引き継がないのと同じ判断）
//   - キャラクターが異なる（補間中にキャラ同士がモーフィングしてしまう）
//   - 現カットと同一のキーフレーム（尺分割で同じキーフレームを共有するカット等。
//     終了フレーム = 開始フレームを強制すると動きが殺される）
func nextCutLastFrameReference(cuts []orchestrator.Cut, i int) string {
	if i+1 >= len(cuts) {
		return ""
	}
	cur := cuts[i]
	next := cuts[i+1]
	ref := strings.TrimSpace(next.KeyframeReference)
	if ref == "" || next.IsSectionStart {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(cur.CharacterID), strings.TrimSpace(next.CharacterID)) {
		return ""
	}
	if ref == strings.TrimSpace(cur.KeyframeReference) {
		return ""
	}
	return ref
}

// videoPrompt builds the prompt used for video generation, appending the guidance that matches
// how Veo will actually interpret the request (image_to_video / reference_to_video /
// video_extension). 生成モードごとに正しい前提（開始フレームあり・参照画像あり・前クリップ
// 継続）を伝えることで、存在しない入力への言及（例: video_extension で「参照画像に合わせろ」）を
// 避けます。VisualAnchor と AudioCue が両方空の場合は従来通り空文字を返し、
// validateVertexVeoRequest の「prompt is required」検証で壊れたレシピを検出できるままにします。
func videoPrompt(cut orchestrator.Cut, mode veoGenerationMode) string {
	anchor := strings.TrimSpace(cut.VisualAnchor)
	cue := strings.TrimSpace(cut.AudioCue)
	if anchor == "" && cue == "" {
		return ""
	}
	parts := make([]string, 0, 3)
	if anchor != "" {
		parts = append(parts, anchor)
	}
	if cue != "" {
		parts = append(parts, "Synchronize motion and camera timing with audio cue: "+cue)
	}
	if guidance := strings.TrimSpace(videoGenGuidance()[string(mode)]); guidance != "" {
		parts = append(parts, guidance)
	}
	return strings.Join(parts, "\n")
}
