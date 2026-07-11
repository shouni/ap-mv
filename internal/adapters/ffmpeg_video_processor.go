package adapters

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"github.com/shouni/ap-mv/internal/ports"
)

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
	defer os.Remove(localVideo)

	localFrame, err := tempFilePath(".jpg")
	if err != nil {
		return "", fmt.Errorf("extract last frame: %w", err)
	}
	defer os.Remove(localFrame)

	// -sseof -1: 末尾1秒手前からデコードを開始し、その中の最後の1フレームだけ書き出す。
	// ファイル全体をデコードするより高速。
	cmd := exec.CommandContext(ctx, p.binary(), "-y", "-sseof", "-1", "-i", localVideo, "-frames:v", "1", "-q:v", "2", localFrame)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("extract last frame: ffmpeg: %w: %s", runErr, out)
	}

	if err := p.uploadFromTemp(ctx, localFrame, destURI, "image/jpeg"); err != nil {
		return "", fmt.Errorf("extract last frame: upload %s: %w", destURI, err)
	}
	return destURI, nil
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
			os.Remove(lp)
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
	defer os.Remove(listFile)

	localOut, err := tempFilePath(".mp4")
	if err != nil {
		return "", fmt.Errorf("concat hard cut: %w", err)
	}
	defer os.Remove(localOut)

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
	defer rc.Close()

	path, err := tempFilePath(ext)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("open temp file: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, rc); err != nil {
		os.Remove(path)
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
	defer f.Close()
	return p.Writer.Write(ctx, destURI, f, remoteio.WithContentType(contentType))
}

// streamCopy は srcURI の内容をローカルへ書き出さず直接 destURI へストリームコピーします。
func (p *FFmpegVideoProcessor) streamCopy(ctx context.Context, srcURI, destURI, contentType string) error {
	rc, err := p.Reader.Open(ctx, srcURI)
	if err != nil {
		return fmt.Errorf("open %s: %w", srcURI, err)
	}
	defer rc.Close()
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
		os.Remove(path)
		return "", fmt.Errorf("close temp file: %w", err)
	}
	return path, nil
}

// writeConcatList は ffmpeg concat demuxer 用のリストファイルを作成し、そのパスを返します。
func writeConcatList(localPaths []string) (string, error) {
	listPath, err := tempFilePath(".txt")
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, p := range localPaths {
		// concat demuxerはシングルクォートを '\'' でエスケープする
		escaped := strings.ReplaceAll(p, "'", `'\''`)
		fmt.Fprintf(&sb, "file '%s'\n", escaped)
	}
	if err := os.WriteFile(listPath, []byte(sb.String()), 0o600); err != nil {
		os.Remove(listPath)
		return "", fmt.Errorf("write concat list: %w", err)
	}
	return listPath, nil
}
