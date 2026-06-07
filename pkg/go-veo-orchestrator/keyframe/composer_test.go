package keyframe

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/shouni/go-veo-orchestrator/ports"
)

// --- Mocks ---

type mockAssetManager struct {
	uploadCount int32
	deleteCount int32
	uploadFunc  func(ctx context.Context, refURL string) (string, error)
}

func (m *mockAssetManager) UploadFile(ctx context.Context, refURL string) (string, error) {
	atomic.AddInt32(&m.uploadCount, 1)
	if m.uploadFunc != nil {
		return m.uploadFunc(ctx, refURL)
	}
	return "https://file-api.google.com/" + refURL, nil
}

func (m *mockAssetManager) DeleteFile(ctx context.Context, fileURI string) error {
	atomic.AddInt32(&m.deleteCount, 1)
	return nil
}

type mockBackend struct {
	isVertex bool
}

func (m *mockBackend) IsVertexAI() bool { return m.isVertex }

func mustNewCharacters(t *testing.T, list []ports.Character) *ports.Characters {
	t.Helper()

	chars, err := ports.NewCharacters(list)
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	return chars
}

// --- Tests ---

func TestVideoComposer_PrepareCharacterResources(t *testing.T) {
	ctx := context.Background()
	assetMgr := &mockAssetManager{}
	backend := &mockBackend{isVertex: false}

	cm := mustNewCharacters(t, []ports.Character{
		{
			ID:           "zundamon",
			Name:         "ずんだもん",
			VisualCues:   []string{"green hair"},
			ReferenceURL: "gs://bucket/zunda.png",
		},
		{
			ID:           "metan",
			Name:         "めたん",
			VisualCues:   []string{"purple hair"},
			ReferenceURL: "gs://bucket/metan.png",
			IsDefault:    true,
		},
	})

	mc, _ := NewVideoComposer(assetMgr, backend, cm)

	keyframes := []ports.Cut{
		{CharacterID: "zundamon"},
		{CharacterID: "unknown"}, // default (metan) が使用される
	}

	err := mc.PrepareCharacterResources(ctx, keyframes)
	if err != nil {
		t.Fatalf("PrepareCharacterResources failed: %v", err)
	}

	if uri := mc.GetCharacterResourceURI("zundamon"); uri == "" {
		t.Error("zundamon resource not cached")
	}
	if uri := mc.GetCharacterResourceURI("metan"); uri == "" {
		t.Error("default character (metan) resource not cached")
	}

	if assetMgr.uploadCount != 2 {
		t.Errorf("Expected 2 uploads, got %d", assetMgr.uploadCount)
	}
}

func TestNewVideoComposer_RequiresCharacters(t *testing.T) {
	_, err := NewVideoComposer(&mockAssetManager{}, &mockBackend{}, nil)
	if err == nil {
		t.Fatal("expected error for nil characters")
	}
}
