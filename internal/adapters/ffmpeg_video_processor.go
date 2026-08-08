package adapters

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/ports"
)

// satavgPattern は ffmpeg の signalstats フィルタが metadata=print で出力する平均彩度
// （0-255スケール）を抽出する正規表現です。
var satavgPattern = regexp.MustCompile(`lavfi\.signalstats\.SATAVG=([0-9.]+)`)

// defaultFFmpegBinary は PATH 上の ffmpeg 実行ファイル名です（Dockerfile で
// /usr/local/bin/ffmpeg に静的リンクバイナリを配置しています）。
const defaultFFmpegBinary = "ffmpeg"

// FFmpegVideoProcessor は ports.VideoProcessor をローカルの ffmpeg バイナリで実装します。
// ap-mv の中で唯一、動画バイナリ処理（フレーム抽出・結合）を担う境界です。
type FFmpegVideoProcessor struct {
	Reader orchestrator.ContentReader
	Writer remoteio.OutputWriter
	// Binary は ffmpeg 実行ファイルのパスです。空の場合は PATH 上の "ffmpeg" を使います。
	Binary string
}

var _ ports.VideoProcessor = (*FFmpegVideoProcessor)(nil)

// NewFFmpegVideoProcessor は FFmpegVideoProcessor を初期化します。
func NewFFmpegVideoProcessor(reader orchestrator.ContentReader, writer remoteio.OutputWriter) *FFmpegVideoProcessor {
	return &FFmpegVideoProcessor{Reader: reader, Writer: writer}
}

func (p *FFmpegVideoProcessor) binary() string {
	if p != nil && strings.TrimSpace(p.Binary) != "" {
		return p.Binary
	}
	return defaultFFmpegBinary
}

// ExtractLastFrame は videoURI の最終フレームを画像として抽出し destURI へアップロードします。
func (p *FFmpegVideoProcessor) ExtractLastFrame(ctx context.Context, videoURI, destURI string) (string, error) {
	if p == nil || p.Reader == nil || p.Writer == nil {
		return "", fmt.Errorf("ffmpeg video processor is not configured")
	}
	localVideo, err := p.downloadToTemp(ctx, videoURI, ".mp4")
	if err != nil {
		return "", fmt.Errorf("extract last frame: download %s: %w", videoURI, err)
	}
	defer func() { _ = os.Remove(localVideo) }()

	localFrame, err := tempFilePath(".png")
	if err != nil {
		return "", fmt.Errorf("extract last frame: %w", err)
	}
	defer func() { _ = os.Remove(localFrame) }()

	// -sseof -3: 末尾3秒手前からデコードを開始する（ファイル全体をデコードするより高速）。
	// -frames:v 1 だとシーク直後に最初にデコードされた1フレーム（末尾から最大3秒手前）を
	// 書き出してしまい、本当の最終フレームとズレる。実ジョブで検証したところ、この
	// ズレは末尾1秒手前の設定でもPSNR ~17dB相当の無視できない差になっていた。
	// -update 1（image2 マルチプレクサの単一ファイル上書きモード）を使い、シーク後に
	// デコードされる残り数秒分の最後の1フレーム（＝真の最終フレーム）が書き込まれた
	// 状態で終わるようにする。PNG(可逆)で出力する。動画自体は既にH.264で非可逆圧縮
	// 済みだが、ここでJPEG等の非可逆フォーマットへさらに変換すると、次チェーンの
	// 参照画像として二重圧縮による劣化が加わってしまうため避ける。
	cmd := exec.CommandContext(ctx, p.binary(), "-y", "-sseof", "-3", "-i", localVideo, "-update", "1", localFrame)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("extract last frame: ffmpeg: %w: %s", runErr, out)
	}

	if err := p.uploadFromTemp(ctx, localFrame, destURI, "image/png"); err != nil {
		return "", fmt.Errorf("extract last frame: upload %s: %w", destURI, err)
	}
	return destURI, nil
}

// ColorMatchSaturation は videoURI の彩度を referenceImageURI の平均彩度に合わせて補正します。
func (p *FFmpegVideoProcessor) ColorMatchSaturation(ctx context.Context, videoURI, referenceImageURI, destURI string) (string, error) {
	if p == nil || p.Reader == nil || p.Writer == nil {
		return "", fmt.Errorf("ffmpeg video processor is not configured")
	}
	localVideo, err := p.downloadToTemp(ctx, videoURI, ".mp4")
	if err != nil {
		return "", fmt.Errorf("color match saturation: download video %s: %w", videoURI, err)
	}
	defer func() { _ = os.Remove(localVideo) }()

	localRef, err := p.downloadToTemp(ctx, referenceImageURI, ".png")
	if err != nil {
		return "", fmt.Errorf("color match saturation: download reference %s: %w", referenceImageURI, err)
	}
	defer func() { _ = os.Remove(localRef) }()

	refSat, err := p.averageSaturation(ctx, localRef)
	if err != nil {
		return "", fmt.Errorf("color match saturation: measure reference: %w", err)
	}
	// 動画は末尾付近をサンプリングする。video_extension は累積動画全体を毎回再生成するため、
	// 先頭フレームは(その回では)まだ新規生成されていない古い区間で、ドリフトが蓄積していない
	// ことがある。ドリフトは各世代で新しく生成される末尾側に現れるため、そこを測らないと
	// 実際のドリフト量を過小評価してしまう（実測: 同じ動画で先頭フレームSATAVG=26.7に対し
	// 末尾1秒手前は35.7、末尾側でしかドリフトを検出できなかった）。
	videoSat, err := p.averageSaturationNearEnd(ctx, localVideo)
	if err != nil {
		return "", fmt.Errorf("color match saturation: measure video: %w", err)
	}
	if videoSat <= 0 {
		return "", fmt.Errorf("color match saturation: video has no measurable saturation")
	}

	// 補正比率。基準画像1枚・動画1フレームだけのサンプルという粗い測定なので、
	// 誤差や構図差による過補正で不自然な見た目にならないよう [0.7, 1.3] にクランプする。
	ratio := refSat / videoSat
	if ratio < 0.7 {
		ratio = 0.7
	} else if ratio > 1.3 {
		ratio = 1.3
	}

	localOut, err := tempFilePath(".mp4")
	if err != nil {
		return "", fmt.Errorf("color match saturation: %w", err)
	}
	defer func() { _ = os.Remove(localOut) }()

	// eq=saturation=<ratio> で全フレームの彩度を一律スケーリングする。音声は再エンコードせず
	// そのままコピーする（映像フィルタのみが対象のため）。
	cmd := exec.CommandContext(ctx, p.binary(), "-y", "-i", localVideo,
		"-vf", fmt.Sprintf("eq=saturation=%.4f", ratio),
		"-c:v", "libx264", "-preset", "fast", "-crf", "18", "-c:a", "copy", localOut)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("color match saturation: ffmpeg encode: %w: %s", runErr, out)
	}

	if err := p.uploadFromTemp(ctx, localOut, destURI, "video/mp4"); err != nil {
		return "", fmt.Errorf("color match saturation: upload %s: %w", destURI, err)
	}
	return destURI, nil
}

// averageSaturation は ffmpeg の signalstats フィルタで、画像（またはシーク済み動画）の
// 先頭フレームの平均彩度(SATAVG, 0-255スケール)を測定します。
func (p *FFmpegVideoProcessor) averageSaturation(ctx context.Context, localPath string) (float64, error) {
	return p.averageSaturationArgs(ctx, "-i", localPath, "-frames:v", "1", "-vf", "signalstats,metadata=print", "-f", "null", "-")
}

// averageSaturationNearEnd は動画の末尾1秒手前のフレームの平均彩度を測定します。
func (p *FFmpegVideoProcessor) averageSaturationNearEnd(ctx context.Context, localVideo string) (float64, error) {
	return p.averageSaturationArgs(ctx, "-sseof", "-1", "-i", localVideo, "-frames:v", "1", "-vf", "signalstats,metadata=print", "-f", "null", "-")
}

func (p *FFmpegVideoProcessor) averageSaturationArgs(ctx context.Context, args ...string) (float64, error) {
	cmd := exec.CommandContext(ctx, p.binary(), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("ffmpeg signalstats: %w: %s", err, out)
	}
	match := satavgPattern.FindSubmatch(out)
	if match == nil {
		return 0, fmt.Errorf("SATAVG not found in ffmpeg signalstats output")
	}
	sat, err := strconv.ParseFloat(string(match[1]), 64)
	if err != nil {
		return 0, fmt.Errorf("parse SATAVG %q: %w", match[1], err)
	}
	return sat, nil
}

// ConcatHardCut は videoURIs をハードカットで結合し destURI へアップロードします。
// 入力が1件だけの場合は再エンコードせずそのままコピーします。
func (p *FFmpegVideoProcessor) ConcatHardCut(ctx context.Context, videoURIs []string, destURI string) (string, error) {
	if p == nil || p.Reader == nil || p.Writer == nil {
		return "", fmt.Errorf("ffmpeg video processor is not configured")
	}
	if len(videoURIs) == 0 {
		return "", fmt.Errorf("concat hard cut: no input videos")
	}
	if len(videoURIs) == 1 {
		if err := p.streamCopy(ctx, videoURIs[0], destURI, "video/mp4"); err != nil {
			return "", fmt.Errorf("concat hard cut: copy single video: %w", err)
		}
		return destURI, nil
	}

	localPaths := make([]string, 0, len(videoURIs))
	defer func() {
		for _, lp := range localPaths {
			_ = os.Remove(lp)
		}
	}()
	for i, uri := range videoURIs {
		localPath, err := p.downloadToTemp(ctx, uri, ".mp4")
		if err != nil {
			return "", fmt.Errorf("concat hard cut: download chain %d (%s): %w", i, uri, err)
		}
		localPaths = append(localPaths, localPath)
	}

	listFile, err := writeConcatList(localPaths)
	if err != nil {
		return "", fmt.Errorf("concat hard cut: %w", err)
	}
	defer func() { _ = os.Remove(listFile) }()

	localOut, err := tempFilePath(".mp4")
	if err != nil {
		return "", fmt.Errorf("concat hard cut: %w", err)
	}
	defer func() { _ = os.Remove(localOut) }()

	// 各チェーンはVeoの別々の生成呼び出しで作られたクリップなので、コーデック/パラメータの
	// 一致を前提にできる "-c copy" は使わず、常に再エンコードする(クリップは数十秒程度なので
	// コストは小さい)。
	cmd := exec.CommandContext(ctx, p.binary(),
		"-y", "-f", "concat", "-safe", "0", "-i", listFile,
		"-c:v", "libx264", "-preset", "veryfast", "-c:a", "aac",
		localOut,
	)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("concat hard cut: ffmpeg: %w: %s", runErr, out)
	}

	if err := p.uploadFromTemp(ctx, localOut, destURI, "video/mp4"); err != nil {
		return "", fmt.Errorf("concat hard cut: upload %s: %w", destURI, err)
	}
	return destURI, nil
}

// downloadToTemp は srcURI の内容をローカル一時ファイルへストリームし、そのパスを返します。
// 呼び出し側が os.Remove で削除する責任を持ちます。
func (p *FFmpegVideoProcessor) downloadToTemp(ctx context.Context, srcURI, ext string) (string, error) {
	rc, err := p.Reader.Open(ctx, srcURI)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", srcURI, err)
	}
	defer func() { _ = rc.Close() }()

	f, err := os.CreateTemp("", "ap-mv-videoproc-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	path := f.Name()
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, rc); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("write temp file: %w", err)
	}
	return path, nil
}

// uploadFromTemp はローカルファイル localPath の内容を destURI へアップロードします。
func (p *FFmpegVideoProcessor) uploadFromTemp(ctx context.Context, localPath, destURI, contentType string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", localPath, err)
	}
	defer func() { _ = f.Close() }()
	return p.Writer.Write(ctx, destURI, f, remoteio.WithContentType(contentType))
}

// streamCopy は srcURI の内容をローカルへ書き出さず直接 destURI へストリームコピーします。
func (p *FFmpegVideoProcessor) streamCopy(ctx context.Context, srcURI, destURI, contentType string) error {
	rc, err := p.Reader.Open(ctx, srcURI)
	if err != nil {
		return fmt.Errorf("open %s: %w", srcURI, err)
	}
	defer func() { _ = rc.Close() }()
	return p.Writer.Write(ctx, destURI, rc, remoteio.WithContentType(contentType))
}

// tempFilePath は空の一時ファイルを作成し、そのパスを返します(ファイルハンドルは閉じます)。
func tempFilePath(ext string) (string, error) {
	f, err := os.CreateTemp("", "ap-mv-videoproc-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close temp file: %w", err)
	}
	return path, nil
}

// writeConcatList は ffmpeg concat demuxer 用のリストファイルを作成し、そのパスを返します。
func writeConcatList(localPaths []string) (string, error) {
	f, err := os.CreateTemp("", "ap-mv-videoproc-*.txt")
	if err != nil {
		return "", fmt.Errorf("create concat list: %w", err)
	}
	listPath := f.Name()
	defer func() { _ = f.Close() }()
	var sb strings.Builder
	for _, p := range localPaths {
		// concat demuxerはシングルクォートを '\'' でエスケープする
		escaped := strings.ReplaceAll(p, "'", `'\''`)
		fmt.Fprintf(&sb, "file '%s'\n", escaped)
	}
	if _, err := f.WriteString(sb.String()); err != nil {
		_ = os.Remove(listPath)
		return "", fmt.Errorf("write concat list: %w", err)
	}
	return listPath, nil
}

// probeStreamsPattern は ffmpeg のストリーム行から種別を拾います。
var probeStreamsPattern = regexp.MustCompile(`Stream #\d+:\d+.*?: (Video|Audio):`)

// probeDurationPattern は ffmpeg のヘッダ出力から総尺（HH:MM:SS.ss）を拾います。
var probeDurationPattern = regexp.MustCompile(`Duration: (\d+):(\d+):(\d+\.\d+)`)

// Probe は動画の実尺と音声トラックの有無を測ります。
//
// ffprobe ではなく ffmpeg を使うのは、このコンテナに置いているのが ffmpeg だけだからです。
// 出力先を null にして解析だけ行わせ、標準エラーに出るヘッダ情報を読み取ります。
func (p *FFmpegVideoProcessor) Probe(ctx context.Context, videoURI string) (ports.VideoStats, error) {
	if p == nil || p.Reader == nil {
		return ports.VideoStats{}, fmt.Errorf("ffmpeg video processor is not configured")
	}

	localPath, err := p.downloadToTemp(ctx, videoURI, ".mp4")
	if err != nil {
		return ports.VideoStats{}, fmt.Errorf("probe: download %s: %w", videoURI, err)
	}
	defer func() { _ = os.Remove(localPath) }()

	// 解析だけを行うため出力は捨てる。-i だけの実行は終了コードが 1 になるので、
	// エラーではなく出力の中身で判断する。
	cmd := exec.CommandContext(ctx, p.binary(), "-i", localPath, "-f", "null", "-")
	out, _ := cmd.CombinedOutput()

	stats, err := parseProbeOutput(out)
	if err != nil {
		return ports.VideoStats{}, fmt.Errorf("probe %s: %w", videoURI, err)
	}
	return stats, nil
}

// parseProbeOutput は ffmpeg のヘッダ出力から尺と音声トラックの有無を読み取ります。
// ffmpeg の実行から切り離した純関数にしているのは、バイナリ無しで解析規則を
// テストできるようにするためです。
func parseProbeOutput(out []byte) (ports.VideoStats, error) {
	stats := ports.VideoStats{}
	for _, match := range probeStreamsPattern.FindAllSubmatch(out, -1) {
		if string(match[1]) == "Audio" {
			stats.HasAudio = true
		}
	}

	match := probeDurationPattern.FindSubmatch(out)
	if match == nil {
		return ports.VideoStats{}, fmt.Errorf("duration not found in ffmpeg output")
	}
	hours, _ := strconv.ParseFloat(string(match[1]), 64)
	minutes, _ := strconv.ParseFloat(string(match[2]), 64)
	seconds, err := strconv.ParseFloat(string(match[3]), 64)
	if err != nil {
		return ports.VideoStats{}, fmt.Errorf("parse duration %q: %w", match[0], err)
	}
	stats.DurationSeconds = hours*3600 + minutes*60 + seconds

	return stats, nil
}
