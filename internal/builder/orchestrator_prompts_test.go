package builder

import (
	"strings"
	"testing"
	"testing/fstest"

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
			DurationSec: 8,
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
