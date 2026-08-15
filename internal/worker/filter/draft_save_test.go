package filter

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/internal/domain"
)

// draftWriter captures what DraftSaveFilter writes so the test can assert on the URI and payload
// without a storage backend.
type draftWriter struct {
	uri  string
	body string
}

func (w *draftWriter) Write(_ context.Context, path string, contentReader io.Reader, _ ...remoteio.WriteOption) error {
	raw, err := io.ReadAll(contentReader)
	if err != nil {
		return err
	}
	w.uri = path
	w.body = string(raw)
	return nil
}

func (w *draftWriter) Delete(context.Context, string) error { return nil }

func draftTestRecipe() *video.Recipe {
	return &video.Recipe{
		ProjectTitle: "draft test",
		MusicRecipe: video.MusicRecipe{
			Title:    "draft test",
			Sections: []video.Section{{Name: "Chorus", Duration: 8, StartSeconds: 0, EndSeconds: 8, Prompt: "lift"}},
		},
		Cuts: []video.Cut{{
			CutIndex:     1,
			SectionIndex: 1,
			VisualAnchor: "rooftop at dawn",
			AudioSync:    video.AudioSync{StartSec: 0, EndSec: 8, DurationSec: 8, AudioCue: "0:00 to 0:08"},
		}},
	}
}

func TestDraftSaveFilterWritesRecipeToDraftPath(t *testing.T) {
	writer := &draftWriter{}
	recipe := draftTestRecipe()

	err := (DraftSaveFilter{}).Execute(context.Background(), &Context{
		State: State{
			Task:        &domain.Task{JobID: "video-draft-20260804-101112-abc", Command: domain.CommandVideoRecipeDraft},
			VideoRecipe: recipe,
			DraftPath:   "gs://bucket/ap-mv/veo/drafts/video-draft-20260804-101112-abc/",
		},
		Services: Services{Writer: writer},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	wantURI := "gs://bucket/ap-mv/veo/drafts/video-draft-20260804-101112-abc/" + domain.VideoDraftFileName
	if writer.uri != wantURI {
		t.Errorf("wrote to %q, want %q", writer.uri, wantURI)
	}
	// 保存した JSON がそのまま mv_from_keyframe_video_recipe の recipe_url になるため、
	// 読み直せることまで確認する。
	decoded, err := domain.DecodeVideoRecipeJSON([]byte(writer.body))
	if err != nil {
		t.Fatalf("saved draft does not decode as a VideoRecipe: %v", err)
	}
	if len(decoded.Cuts) != len(recipe.Cuts) {
		t.Errorf("decoded cuts = %d, want %d", len(decoded.Cuts), len(recipe.Cuts))
	}
}

// TestDraftSaveRoundTripKeepsCutPlan is the integration check the whole draft gate rests on:
// what the user reviewed in the draft must be what actually gets generated. It walks the real
// path — SceneSplit -> DraftSave -> the saved JSON -> RecipeLoad's decoder -> SceneSplit again
// (mv_from_keyframe_video_recipe re-runs it) — and asserts the cut plan comes out identical.
// A flag that fails to serialize, or a second split that re-plans, would silently change the
// keyframe count and the chain layout between review and generation.
func TestDraftSaveRoundTripKeepsCutPlan(t *testing.T) {
	sceneSplit := SceneSplitFilter{UsePreviousVideo: true}
	recipe := &video.Recipe{
		ProjectTitle: "round trip",
		MusicRecipe: video.MusicRecipe{
			Title:    "round trip",
			Sections: []video.Section{{Name: "Verse", Duration: 30, StartSeconds: 0, EndSeconds: 30, Prompt: "verse"}},
		},
		Cuts: []video.Cut{
			{CutIndex: 1, SectionIndex: 1, VisualAnchor: "rooftop", AudioSync: video.AudioSync{StartSec: 0, DurationSec: 30, AudioCue: "0:00 to 0:30"}},
		},
	}
	task := &domain.Task{JobID: "video-draft-20260804-101112-abc", Command: domain.CommandVideoRecipeDraft}
	newContext := func() *Context {
		return &Context{State: State{Task: task, VideoRecipe: recipe}}
	}

	if err := sceneSplit.Execute(context.Background(), newContext()); err != nil {
		t.Fatalf("scene split error = %v", err)
	}
	planned := append([]video.Cut(nil), recipe.Cuts...)

	writer := &draftWriter{}
	saveContext := newContext()
	saveContext.DraftPath = "gs://bucket/drafts/" + task.JobID + "/"
	saveContext.Writer = writer
	if err := (DraftSaveFilter{}).Execute(context.Background(), saveContext); err != nil {
		t.Fatalf("draft save error = %v", err)
	}

	// RecipeLoadFilter が recipe_url から読むときと同じデコード経路を通す。
	_, loaded, err := domain.UnmarshalRecipeOrVideoRecipe([]byte(writer.body))
	if err != nil {
		t.Fatalf("saved draft did not load as a VideoRecipe: %v", err)
	}
	if loaded == nil {
		t.Fatal("saved draft decoded as a MusicRecipe; want a VideoRecipe")
	}

	reloadContext := &Context{State: State{Task: task, VideoRecipe: loaded}}
	if err := sceneSplit.Execute(context.Background(), reloadContext); err != nil {
		t.Fatalf("scene split after reload error = %v", err)
	}

	if len(loaded.Cuts) != len(planned) {
		t.Fatalf("cuts after round trip = %d, want %d", len(loaded.Cuts), len(planned))
	}
	for i := range planned {
		want, got := planned[i], loaded.Cuts[i]
		if want.DurationSec != got.DurationSec || want.StartSec != got.StartSec || want.EndSec != got.EndSec {
			t.Errorf("cut[%d] timing changed: planned %v..%v (%vs), reloaded %v..%v (%vs)",
				i, want.StartSec, want.EndSec, want.DurationSec, got.StartSec, got.EndSec, got.DurationSec)
		}
		if want.IsChainStart != got.IsChainStart || want.IsSectionStart != got.IsSectionStart {
			t.Errorf("cut[%d] chain flags changed: planned chain=%v section=%v, reloaded chain=%v section=%v",
				i, want.IsChainStart, want.IsSectionStart, got.IsChainStart, got.IsSectionStart)
		}
		if want.VisualAnchor != got.VisualAnchor {
			t.Errorf("cut[%d].VisualAnchor changed:\n planned  = %q\n reloaded = %q", i, want.VisualAnchor, got.VisualAnchor)
		}
	}
}

// TestDraftSaveFilterRequiresDraftPath pins that a missing draft path fails loudly. Succeeding
// without writing would leave a "completed" draft job that never appears in the drafts list.
func TestDraftSaveFilterRequiresDraftPath(t *testing.T) {
	err := (DraftSaveFilter{}).Execute(context.Background(), &Context{
		State:    State{Task: &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeDraft}, VideoRecipe: draftTestRecipe()},
		Services: Services{Writer: &draftWriter{}},
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want an error when DraftPath is empty")
	}
	if !strings.Contains(err.Error(), "draft path") {
		t.Errorf("Execute() error = %v, want it to mention the missing draft path", err)
	}
}

// TestDraftSaveFilterRejectsInvalidRecipe pins that validation happens before the write, so a
// recipe that would fail during keyframe generation never reaches the drafts list.
func TestDraftSaveFilterRejectsInvalidRecipe(t *testing.T) {
	recipe := draftTestRecipe()
	recipe.Cuts = nil

	writer := &draftWriter{}
	err := (DraftSaveFilter{}).Execute(context.Background(), &Context{
		State: State{
			Task:        &domain.Task{JobID: "job-1", Command: domain.CommandVideoRecipeDraft},
			VideoRecipe: recipe,
			DraftPath:   "gs://bucket/drafts/job-1/",
		},
		Services: Services{Writer: writer},
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want a validation error for a recipe with no cuts")
	}
	if writer.uri != "" {
		t.Errorf("wrote %q despite validation failure; want no write", writer.uri)
	}
}
