package prompt

import (
	"strings"
	"testing"

	"github.com/shouni/genai-kit/music"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/video"
)

const tsumugiLook = "Tsumugi, a girl with short brownish-orange hair with a right-side ponytail, bright light-blue eyes, wearing an oversized mustard-yellow cardigan, a dark grey collared shirt with a yellow necktie, and a pink-and-black plaid pleated skirt"

func flowMusicRecipe() *video.Recipe {
	return &video.Recipe{
		Description:    "A summer EDM anime video.",
		LocationAnchor: "a coastal boulevard",
		AspectRatio:    "9:16",
		MusicRecipe: video.MusicRecipe{
			Title: "Blast Summer",
			Mood:  "Euphoric EDM",
			Tempo: 128,
			Sections: []music.Section{
				{Name: "Verse"},
				{Name: "Chorus"},
			},
		},
		Cuts: []video.Cut{
			{CutIndex: 1, SectionIndex: 1, CharacterID: "tsumugi", StartSec: 12, EndSec: 20, VisualAnchor: "Close-up of " + tsumugiLook + ", sprinting down the boulevard.", AudioCue: "Verse - steady kick"},
			{CutIndex: 2, SectionIndex: 2, CharacterID: "tsumugi", StartSec: 40, EndSec: 48, VisualAnchor: "Wide shot of " + tsumugiLook + ", singing toward the camera.", AudioCue: "Chorus - drop", Dialogue: "弾けろ\nブラスト・サマー"},
			{CutIndex: 3, SectionIndex: 2, CharacterID: "tsumugi", StartSec: 48, EndSec: 55, VisualAnchor: "The empty boulevard at night.", AudioCue: "Chorus - '夜を走れ'", Dialogue: "夜を走れ"},
		},
	}
}

func flowMusicCharacters(t *testing.T) *characterkit.Characters {
	t.Helper()
	characters, err := characterkit.NewCharacters([]characterkit.Character{{ID: "tsumugi", Name: "Tsumugi", VisualCues: []string{"orange hair"}, ReferenceURL: "gs://bucket/tsumugi.png", IsDefault: true}})
	if err != nil {
		t.Fatalf("NewCharacters() error = %v", err)
	}
	return characters
}

// TestFlowMusicBriefStatesTheCharacterOnce verifies the appearance every visual_anchor repeats
// is stated once up front and replaced by the character's name in the shots. A full MV has
// dozens of cuts; repeating it per shot multiplies the text by the cut count.
func TestFlowMusicBriefStatesTheCharacterOnce(t *testing.T) {
	got := FlowMusicBrief(flowMusicRecipe(), flowMusicCharacters(t))

	if n := strings.Count(got, "short brownish-orange hair"); n != 1 {
		t.Errorf("appearance stated %d times, want once:\n%s", n, got)
	}
	for _, want := range []string{
		"Create a music video for the attached song \"Blast Summer\".",
		"Tempo: 128 BPM\n",
		"Aspect ratio: 9:16\n",
		"Setting: a coastal boulevard\n",
		"Character Tsumugi: a girl with short brownish-orange hair",
		"pleated skirt. Keep this appearance",
		"[0:12 - 0:20] Verse: Close-up of Tsumugi, sprinting down the boulevard.\n  Music: Verse - steady kick\n",
		"[0:40 - 0:48] Chorus: Wide shot of Tsumugi, singing toward the camera.\n  Music: Chorus - drop\n  Lyrics: 弾けろ / ブラスト・サマー\n",
		// 共通の描写を持たないカットはそのまま残り、全体の畳み込みも止めない。
		"[0:48 - 0:55] Chorus: The empty boulevard at night.\n",
		"- No text, captions, subtitles, lyrics, logos, or watermarks in frame.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("brief missing %q:\n%s", want, got)
		}
	}
	// audio_cue が歌詞を引用しているカットでは、同じ行を Lyrics で繰り返さない。
	if strings.Contains(got, "Lyrics: 夜を走れ") {
		t.Errorf("lyrics already quoted in the audio cue were repeated:\n%s", got)
	}
}

// TestFlowMusicBriefLeavesShortOverlapsAlone verifies anchors that merely share a few words are
// not folded: replacing a short coincidental match would mangle the shots.
func TestFlowMusicBriefLeavesShortOverlapsAlone(t *testing.T) {
	recipe := &video.Recipe{Cuts: []video.Cut{
		{CutIndex: 1, CharacterID: "tsumugi", VisualAnchor: "Tsumugi, a girl in a cardigan, runs."},
		{CutIndex: 2, CharacterID: "tsumugi", VisualAnchor: "Tsumugi, a girl in a cardigan, jumps."},
	}}
	got := FlowMusicBrief(recipe, flowMusicCharacters(t))

	if strings.Contains(got, "Character Tsumugi:") {
		t.Errorf("short overlap was folded:\n%s", got)
	}
	if !strings.Contains(got, "Tsumugi, a girl in a cardigan, runs.") {
		t.Errorf("anchor was altered:\n%s", got)
	}
}

func TestFlowMusicBriefDoesNotMutateTheRecipe(t *testing.T) {
	recipe := flowMusicRecipe()
	before := recipe.Cuts[0].VisualAnchor

	FlowMusicBrief(recipe, flowMusicCharacters(t))

	if recipe.Cuts[0].VisualAnchor != before {
		t.Errorf("recipe was mutated: %q", recipe.Cuts[0].VisualAnchor)
	}
}
