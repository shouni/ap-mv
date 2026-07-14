package builder

import (
	"strings"
	"testing"
	"testing/fstest"

	characterkit "github.com/shouni/go-character-kit/character"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
)

// TestScriptPromptBuildUsesDefaultPromptAsset verifies that script prompts use the bundled default template.
func TestScriptPromptBuildUsesDefaultPromptAsset(t *testing.T) {
	prompt, err := newScriptPrompt()
	if err != nil {
		t.Fatalf("newScriptPrompt() error = %v", err)
	}

	got, err := prompt.Build("compose", scriptPromptTestData("青い光の中で走る主人公"))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	for _, want := range []string{
		"## Video Worldview",
		"compose",
		"青い光の中で走る主人公",
		`"music_recipe"`,
		`"character_id": ""`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt does not contain %q:\n%s", want, got)
		}
	}
}

// TestScriptPromptBuildUsesModeTemplateWhenAvailable verifies that mode-specific templates take precedence.
func TestScriptPromptBuildUsesModeTemplateWhenAvailable(t *testing.T) {
	templates, err := loadPromptTemplates(fstest.MapFS{
		"prompts/default.md": {Data: []byte("default {{.Mode}} {{.SourceRecipeJSON}}")},
		"prompts/compose.md": {Data: []byte("compose template {{.Mode}} {{.SourceRecipeJSON}}")},
	}, "prompts")
	if err != nil {
		t.Fatalf("loadPromptTemplates() error = %v", err)
	}
	prompt, err := newScriptPromptFromTemplates(templates)
	if err != nil {
		t.Fatalf("newScriptPromptFromTemplates() error = %v", err)
	}

	got, err := prompt.Build("compose", scriptPromptTestData("source"))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !strings.Contains(got, "compose template compose") || !strings.Contains(got, "source") {
		t.Fatalf("Build() = %q, want compose template", got)
	}
}

// TestScriptPromptBuildFallsBackToDefaultTemplate verifies that missing mode templates fall back to the default template.
func TestScriptPromptBuildFallsBackToDefaultTemplate(t *testing.T) {
	templates, err := loadPromptTemplates(fstest.MapFS{
		"prompts/default.md": {Data: []byte("default {{.Mode}} {{.SourceRecipeJSON}}")},
	}, "prompts")
	if err != nil {
		t.Fatalf("loadPromptTemplates() error = %v", err)
	}
	prompt, err := newScriptPromptFromTemplates(templates)
	if err != nil {
		t.Fatalf("newScriptPromptFromTemplates() error = %v", err)
	}

	got, err := prompt.Build("unknown", scriptPromptTestData("source"))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !strings.Contains(got, "default unknown") || !strings.Contains(got, "source") {
		t.Fatalf("Build() = %q, want default fallback with original mode", got)
	}
}

// TestScriptPromptBuildUsesConfiguredVisualMode verifies that the selected visual mode is rendered into the script prompt.
func TestScriptPromptBuildUsesConfiguredVisualMode(t *testing.T) {
	templates, err := loadPromptTemplates(fstest.MapFS{
		"prompts/default.md": {Data: []byte("default {{.Mode}} {{.SourceRecipeJSON}} {{.VisualPrompt}}")},
	}, "prompts")
	if err != nil {
		t.Fatalf("loadPromptTemplates() error = %v", err)
	}
	prompt, err := newScriptPromptFromTemplates(templates, map[string]string{
		"default":      "default visual",
		"sparkle_rock": "sparkle visual",
	})
	if err != nil {
		t.Fatalf("newScriptPromptFromTemplates() error = %v", err)
	}
	prompt.visualMode = "sparkle_rock"

	got, err := prompt.Build("video_recipe_create", scriptPromptTestData("source"))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !strings.Contains(got, "sparkle visual") {
		t.Fatalf("Build() = %q, want selected visual prompt", got)
	}
}

// TestFormatSourceRecipeJSONDoesNotMutateSource verifies prompt formatting does not normalize the caller's recipe in place.
func TestFormatSourceRecipeJSONDoesNotMutateSource(t *testing.T) {
	recipe := &orchestrator.VideoRecipe{
		MusicRecipe: orchestrator.MusicRecipe{
			Title: "source",
			Sections: []orchestrator.Section{{
				Name:     "Verse",
				Duration: 8,
				Prompt:   "quiet opening",
			}},
		},
		Cuts: []orchestrator.Cut{{
			AudioSync: orchestrator.AudioSync{DurationSec: 8},
		}},
	}

	got, err := formatSourceRecipeJSON(recipe)
	if err != nil {
		t.Fatalf("formatSourceRecipeJSON() error = %v", err)
	}
	if !strings.Contains(got, `"cut_index": 1`) {
		t.Fatalf("formatted recipe did not normalize cut index:\n%s", got)
	}
	if recipe.ProjectTitle != "" {
		t.Fatalf("ProjectTitle mutated to %q", recipe.ProjectTitle)
	}
	if recipe.Cuts[0].CutIndex != 0 || recipe.Cuts[0].EndSec != 0 {
		t.Fatalf("source cut mutated: %#v", recipe.Cuts[0])
	}
}

// TestScriptPromptRequiresSingleProtagonistAnchors verifies the bundled script prompt forbids
// multi-person visual_anchor scenes. Anchors flow verbatim into both keyframe generation
// ("Scene: ...") and the Veo video prompt, and both downstream layers enforce exactly one
// character per cut — so an anchor describing classmates/crowds would produce a
// self-contradicting prompt (the other entry point of the same failure class as the
// multi-view reference sheet regression below).
func TestScriptPromptRequiresSingleProtagonistAnchors(t *testing.T) {
	prompt, err := newScriptPrompt()
	if err != nil {
		t.Fatalf("newScriptPrompt() error = %v", err)
	}

	got, err := prompt.Build("compose", scriptPromptTestData("source"))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	for _, want := range []string{
		"single protagonist alone",
		"exactly one character per cut",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt does not contain %q:\n%s", want, got)
		}
	}
}

// TestKeyframePromptBuildCutWarnsAgainstMultiViewReferenceSheets is a regression test: character
// reference images are often multi-pose turnaround sheets (front/side/back in one image), and
// without an explicit instruction the image model sometimes reproduces that multi-figure layout
// instead of treating it as one character's identity/style reference (observed in
// video-recipe-20260711-212833-438dd22e71d3 cut 1: two near-identical Tsumugi figures generated
// side by side). The system prompt must explicitly rule this out.
func TestKeyframePromptBuildCutWarnsAgainstMultiViewReferenceSheets(t *testing.T) {
	p := keyframePrompt{styleSuffix: "Japanese anime style, cel-shaded"}
	char := &characterkit.Character{Name: "Tsumugi", VisualCues: []string{"twin tails", "green eyes"}}
	cut := orchestrator.Cut{CutIndex: 1, VisualAnchor: "sitting in a train car"}

	_, systemPrompt := p.BuildCut(cut, char)

	for _, want := range []string{
		"exactly one character",
		"multiple angles",
		"not multiple people",
		"never depict more than one character",
		"No text",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("BuildCut() systemPrompt missing %q:\n%s", want, systemPrompt)
		}
	}
}

// TestKeyframePromptBuildEditReinforcesCharacterAndStyle verifies that editing an existing
// keyframe still carries the character identity and style suffix reinforcement that BuildCut
// gives full generation, plus an explicit "keep everything else the same" instruction and a
// "no text" system prompt — so edits don't drift the art style like a bare edit instruction would.
func TestKeyframePromptBuildEditReinforcesCharacterAndStyle(t *testing.T) {
	p := keyframePrompt{styleSuffix: "Japanese anime style, cel-shaded"}
	char := &characterkit.Character{Name: "Tsumugi", VisualCues: []string{"twin tails", "green eyes"}}
	cut := orchestrator.Cut{CutIndex: 2}

	userPrompt, systemPrompt := p.BuildEdit(cut, char, "腕には絆創膏を1〜2枚のみにしてください")

	for _, want := range []string{
		"腕には絆創膏を1〜2枚のみにしてください",
		"Character: Tsumugi",
		"twin tails, green eyes",
		"Japanese anime style, cel-shaded",
		"Keep the composition, pose, background, and art style",
	} {
		if !strings.Contains(userPrompt, want) {
			t.Fatalf("BuildEdit() userPrompt missing %q:\n%s", want, userPrompt)
		}
	}
	if !strings.Contains(systemPrompt, "No text") {
		t.Fatalf("BuildEdit() systemPrompt = %q, want text/caption exclusion", systemPrompt)
	}
}

// TestAllBundledVisualModesRender verifies that every bundled visual mode template renders without error
// when given a fully populated recipe, catching field-name mismatches at test time rather than runtime.
func TestAllBundledVisualModesRender(t *testing.T) {
	prompt, err := newScriptPrompt()
	if err != nil {
		t.Fatalf("newScriptPrompt() error = %v", err)
	}
	data := &orchestrator.TemplateData{
		SourceRecipe: &orchestrator.VideoRecipe{
			MusicRecipe: orchestrator.MusicRecipe{
				Title:       "Test Song",
				Theme:       "journey",
				Mood:        "energetic",
				Tempo:       160,
				Key:         "A minor",
				Instruments: []string{"electric guitar", "drums", "bass"},
				Sections: []orchestrator.Section{
					{Name: "Verse", Duration: 8, StartSeconds: 0, EndSeconds: 8, Prompt: "quiet opening"},
					{Name: "Chorus", Duration: 16, StartSeconds: 8, EndSeconds: 24, Prompt: "full power"},
				},
				Lyrics: &orchestrator.Lyrics{
					Hook:      "夜を駆け抜けろ",
					Keywords:  []string{"夜", "疾走", "光"},
					Narrative: "夜の街を走り続ける主人公",
				},
			},
		},
	}
	modes := []string{"default", "girls_metal", "sparkle_rock", "techno_melancholic"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			got, err := prompt.Build(mode, data)
			if err != nil {
				t.Fatalf("Build(%q) error = %v", mode, err)
			}
			if got == "" {
				t.Fatalf("Build(%q) returned empty prompt", mode)
			}
		})
	}
}

// TestScriptPromptBuildGroundsVisualAnchorInDefaultCharacter verifies that when characters are
// wired into the script prompt, the default character's name and visual cues are rendered into
// the prompt so the LLM writing each cut's visual_anchor has the same appearance grounding that
// keyframe generation later uses — otherwise cuts can drift from the character sheet (e.g. a cut
// describing "short dark hair" for a character actually defined with a long auburn ponytail).
func TestScriptPromptBuildGroundsVisualAnchorInDefaultCharacter(t *testing.T) {
	prompt, err := newScriptPrompt()
	if err != nil {
		t.Fatalf("newScriptPrompt() error = %v", err)
	}
	characters, err := characterkit.NewCharacters([]characterkit.Character{
		{
			ID:           "tsumugi",
			Name:         "Tsumugi",
			VisualCues:   []string{"short brownish-orange hair with a right-side ponytail", "bright light-blue eyes"},
			ReferenceURL: "https://example.com/tsumugi.png",
			IsDefault:    true,
		},
	})
	if err != nil {
		t.Fatalf("characterkit.NewCharacters() error = %v", err)
	}
	prompt.characters = characters

	got, err := prompt.Build("sparkle_rock", scriptPromptTestData("source"))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	for _, want := range []string{
		"Protagonist (Tsumugi)",
		"short brownish-orange hair with a right-side ponytail",
		"bright light-blue eyes",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Build() missing %q:\n%s", want, got)
		}
	}
}

// TestScriptPromptBuildOmitsProtagonistBlockWithoutCharacters verifies that Build still renders
// cleanly (no stray template output) when no characters are configured, preserving existing
// behavior for callers that don't wire a character repository.
func TestScriptPromptBuildOmitsProtagonistBlockWithoutCharacters(t *testing.T) {
	prompt, err := newScriptPrompt()
	if err != nil {
		t.Fatalf("newScriptPrompt() error = %v", err)
	}

	got, err := prompt.Build("sparkle_rock", scriptPromptTestData("source"))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if strings.Contains(got, "Protagonist") {
		t.Fatalf("Build() rendered Protagonist block without configured characters:\n%s", got)
	}
}

func scriptPromptTestData(title string) *orchestrator.TemplateData {
	return &orchestrator.TemplateData{
		SourceRecipe: &orchestrator.VideoRecipe{
			MusicRecipe: orchestrator.MusicRecipe{
				Title: title,
				Sections: []orchestrator.Section{{
					Name:     "Verse",
					Duration: 8,
					Prompt:   title,
				}},
			},
		},
	}
}
