package adapters

import (
	"math"
	"os"
	"strings"
	"testing"
)

// TestParseProbeOutput は、ffmpeg ヘッダ出力からの尺・音声トラック解析を検証します。
// 実際の ffmpeg 出力（-i のヘッダ部分）を模した入力に対する規則の回帰テストで、
// バイナリ無しで走ります。
func TestParseProbeOutput(t *testing.T) {
	out := []byte(`Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'final.mp4':
  Duration: 00:03:07.40, start: 0.000000, bitrate: 5561 kb/s
  Stream #0:0[0x1](und): Video: h264 (High) (avc1 / 0x31637661), yuv420p(progressive), 1920x1080
  Stream #0:1[0x2](und): Audio: aac (LC) (mp4a / 0x6134706D), 48000 Hz, stereo, fltp, 317 kb/s`)

	stats, err := parseProbeOutput(out)
	if err != nil {
		t.Fatalf("parseProbeOutput() error = %v", err)
	}
	if !stats.HasAudio {
		t.Error("HasAudio = false, want true")
	}
	if want := 187.40; math.Abs(stats.DurationSeconds-want) > 0.001 {
		t.Errorf("DurationSeconds = %v, want %v", stats.DurationSeconds, want)
	}
}

func TestParseProbeOutputVideoOnly(t *testing.T) {
	out := []byte(`  Duration: 00:00:08.00, start: 0.000000, bitrate: 5561 kb/s
  Stream #0:0[0x1](und): Video: h264 (High) (avc1 / 0x31637661)`)

	stats, err := parseProbeOutput(out)
	if err != nil {
		t.Fatalf("parseProbeOutput() error = %v", err)
	}
	if stats.HasAudio {
		t.Error("HasAudio = true, want false (video only)")
	}
	if stats.DurationSeconds != 8 {
		t.Errorf("DurationSeconds = %v, want 8", stats.DurationSeconds)
	}
}

func TestParseProbeOutputWithoutDuration(t *testing.T) {
	if _, err := parseProbeOutput([]byte("garbage output")); err == nil {
		t.Fatal("expected error when duration line is missing")
	}
}

// TestWriteConcatList は concat demuxer リストのクォートエスケープを検証します。
// シングルクォートを含むパスは '\” 形式でエスケープしないと ffmpeg 側で壊れます。
func TestWriteConcatList(t *testing.T) {
	listPath, err := writeConcatList([]string{"/tmp/normal.mp4", "/tmp/it's here.mp4"})
	if err != nil {
		t.Fatalf("writeConcatList() error = %v", err)
	}
	defer func() { _ = os.Remove(listPath) }()

	data, err := os.ReadFile(listPath)
	if err != nil {
		t.Fatalf("read list: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "file '/tmp/normal.mp4'\n") {
		t.Errorf("plain path missing: %q", content)
	}
	if !strings.Contains(content, `file '/tmp/it'\''s here.mp4'`) {
		t.Errorf("quoted path not escaped for concat demuxer: %q", content)
	}
}
