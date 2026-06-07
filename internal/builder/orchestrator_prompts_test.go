package builder

import (
	"strings"
	"testing"

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
