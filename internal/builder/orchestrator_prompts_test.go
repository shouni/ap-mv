package builder

import (
	"strings"
	"testing"
	"testing/fstest"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
)

func TestScriptPromptBuildUsesDefaultPromptAsset(t *testing.T) {
	prompt, err := newScriptPrompt()
	if err != nil {
		t.Fatalf("newScriptPrompt() error = %v", err)
	}

	got, err := prompt.Build("compose", &orchestrator.TemplateData{InputText: "青い光の中で走る主人公"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	for _, want := range []string{
		"## Video Worldview",
		"compose",
		"青い光の中で走る主人公",
		`"character_id": "default"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt does not contain %q:\n%s", want, got)
		}
	}
}

func TestScriptPromptBuildUsesModeTemplateWhenAvailable(t *testing.T) {
	templates, err := loadPromptTemplates(fstest.MapFS{
		"prompts/default.md": {Data: []byte("default {{.Mode}} {{.InputText}}")},
		"prompts/compose.md": {Data: []byte("compose template {{.Mode}} {{.InputText}}")},
	}, "prompts")
	if err != nil {
		t.Fatalf("loadPromptTemplates() error = %v", err)
	}
	prompt, err := newScriptPromptFromTemplates(templates)
	if err != nil {
		t.Fatalf("newScriptPromptFromTemplates() error = %v", err)
	}

	got, err := prompt.Build("compose", &orchestrator.TemplateData{InputText: "source"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !strings.Contains(got, "compose template compose source") {
		t.Fatalf("Build() = %q, want compose template", got)
	}
}

func TestScriptPromptBuildFallsBackToDefaultTemplate(t *testing.T) {
	templates, err := loadPromptTemplates(fstest.MapFS{
		"prompts/default.md": {Data: []byte("default {{.Mode}} {{.InputText}}")},
	}, "prompts")
	if err != nil {
		t.Fatalf("loadPromptTemplates() error = %v", err)
	}
	prompt, err := newScriptPromptFromTemplates(templates)
	if err != nil {
		t.Fatalf("newScriptPromptFromTemplates() error = %v", err)
	}

	got, err := prompt.Build("unknown", &orchestrator.TemplateData{InputText: "source"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !strings.Contains(got, "default unknown source") {
		t.Fatalf("Build() = %q, want default fallback with original mode", got)
	}
}
