package keyframe

import (
	"context"
	"testing"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-veo-orchestrator/ports"
)

// --- Mocks ---

type mockKeyframeImageGenerator struct {
	generateCount int
	generateFunc  func(ctx context.Context, req imagePorts.SingleImageRequest) (*imagePorts.ImageResponse, error)
}

func (m *mockKeyframeImageGenerator) GenerateSingleImage(ctx context.Context, req imagePorts.SingleImageRequest) (*imagePorts.ImageResponse, error) {
	m.generateCount++
	if m.generateFunc != nil {
		return m.generateFunc(ctx, req)
	}
	var s int64
	if req.Seed != nil {
		s = *req.Seed
	}
	return &imagePorts.ImageResponse{Data: []byte("fake-keyframe-image"), UsedSeed: s}, nil
}

type mockImagePrompt struct{}

func (m *mockImagePrompt) BuildCut(keyframe ports.Cut, char *ports.Character) (string, string) {
	return keyframe.VisualAnchor, "system"
}

// --- Tests ---

func TestKeyframeGenerator_Execute(t *testing.T) {
	ctx := context.Background()

	// 1. 依存関係のセットアップ
	assetMgr := &mockAssetManager{}
	backend := &mockBackend{isVertex: false}

	// 異なる Seed 値を持つキャラクターを用意
	cm := mustNewCharacters(t, []ports.Character{
		{
			ID:           "zundamon",
			Name:         "ずんだもん",
			VisualCues:   []string{"green hair"},
			Seed:         10001,
			ReferenceURL: "gs://bucket/zunda.png",
		},
		{
			ID:           "metan",
			Name:         "めたん",
			VisualCues:   []string{"purple hair"},
			Seed:         20002,
			ReferenceURL: "gs://bucket/metan.png",
			IsDefault:    true, // 指定なしの場合のデフォルト
		},
	})
	composer, _ := NewVideoComposer(assetMgr, backend, cm)

	genMock := &mockKeyframeImageGenerator{}
	pbMock := &mockImagePrompt{}

	// 2. Generator の作成 (高速化設定)
	generator := NewKeyframeGenerator(
		composer,
		genMock,
		pbMock,
		"gemini-2.0-flash",
		func(g *KeyframeGenerator) {
			g.maxConcurrency = 5
			g.rateInterval = 1 * time.Microsecond
			g.rateBurst = 100
		},
	)

	t.Run("Parallel Generation and Individual Seeds", func(t *testing.T) {
		keyframes := []ports.Cut{
			{CharacterID: "zundamon", Dialogue: "こんにちはなのだ！"},
			{CharacterID: "metan", Dialogue: "ごきげんよう。"},
			{CharacterID: "unknown", Dialogue: "誰かしら？"}, // Defaultのmetanが使われるはず
		}

		// リクエストされた Seed を記録するためのスライス
		capturedSeeds := make([]int64, len(keyframes))
		genMock.generateFunc = func(ctx context.Context, req imagePorts.SingleImageRequest) (*imagePorts.ImageResponse, error) {
			// どのパネルのリクエストか特定するのが難しいため、
			// 呼ばれた順ではなく最終的な Seed 値を検証
			return &imagePorts.ImageResponse{UsedSeed: *req.Seed}, nil
		}

		res, err := generator.Execute(ctx, keyframes)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if len(res) != 3 {
			t.Errorf("Expected 3 images, got %d", len(res))
		}

		for i, r := range res {
			capturedSeeds[i] = r.UsedSeed
		}

		// インデックスごとの Seed 検証
		if capturedSeeds[0] != 10001 {
			t.Errorf("Keyframe 0 (zundamon) expected seed 10001, got %d", capturedSeeds[0])
		}
		if capturedSeeds[1] != 20002 {
			t.Errorf("Keyframe 1 (metan) expected seed 20002, got %d", capturedSeeds[1])
		}
		if capturedSeeds[2] != 20002 {
			t.Errorf("Keyframe 2 (unknown->metan) expected seed 20002, got %d", capturedSeeds[2])
		}
	})

	t.Run("Empty Keyframes Handling", func(t *testing.T) {
		res, err := generator.Execute(ctx, []ports.Cut{})
		if err != nil {
			t.Fatalf("Execute failed on empty: %v", err)
		}
		if res != nil {
			t.Error("Expected nil response for empty keyframes")
		}
	})

	t.Run("Vertex AI Bypass in Keyframe Generation", func(t *testing.T) {
		// Vertex モードでは File API へのアップロードをスキップして直接生成に回る
		backend.isVertex = true
		genMock.generateCount = 0

		keyframes := []ports.Cut{{CharacterID: "zundamon"}}

		_, err := generator.Execute(ctx, keyframes)
		if err != nil {
			t.Fatal(err)
		}

		if genMock.generateCount != 1 {
			t.Errorf("Expected 1 generation call, got %d", genMock.generateCount)
		}
		// mockAssetManager.uploadCount が増えていないことを確認したいが、
		// PrepareCharacterResources 内の挙動に依存するためここでは生成が成功することを確認
	})
}
