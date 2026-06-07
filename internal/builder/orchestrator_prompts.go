package builder

import (
	"fmt"
	"io/fs"
	"strings"

	promptkit "github.com/shouni/go-prompt-kit/prompts"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"ap-mv/assets"
)

const defaultPromptMode = "default"

type scriptPrompt struct {
	builder *promptkit.Builder
}

type scriptPromptData struct {
	Mode      string
	InputText string
}

func newScriptPrompt() (*scriptPrompt, error) {
	content, err := fs.ReadFile(assets.Prompts, "prompts/default.md")
	if err != nil {
		return nil, fmt.Errorf("read default script prompt: %w", err)
	}
	builder, err := promptkit.NewBuilder(map[string]string{
		defaultPromptMode: string(content),
	})
	if err != nil {
		return nil, err
	}
	return &scriptPrompt{builder: builder}, nil
}

func (p *scriptPrompt) Build(mode string, data *orchestrator.TemplateData) (string, error) {
	if data == nil || strings.TrimSpace(data.InputText) == "" {
		return "", fmt.Errorf("input text is required")
	}
	if p == nil || p.builder == nil {
		return "", fmt.Errorf("script prompt builder is not configured")
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = defaultPromptMode
	}
	return p.builder.Build(defaultPromptMode, scriptPromptData{
		Mode:      mode,
		InputText: strings.TrimSpace(data.InputText),
	})
}

type keyframePrompt struct {
	styleSuffix string
}

func (p keyframePrompt) BuildCut(cut orchestrator.Cut, char *orchestrator.Character) (string, string) {
	character := "the main character"
	if char != nil {
		character = char.Name
	}
	cues := ""
	if char != nil && len(char.VisualCues) > 0 {
		cues = strings.Join(char.VisualCues, ", ")
	}
	userPrompt := strings.Join(nonEmptyStrings(
		fmt.Sprintf("Create a clean keyframe image for cut %d.", cut.CutIndex),
		"Character: "+character,
		"Character visual cues: "+cues,
		"Scene: "+strings.TrimSpace(cut.VisualAnchor),
		"Music timing: "+strings.TrimSpace(cut.AudioCue),
		strings.TrimSpace(p.styleSuffix),
	), "\n")
	systemPrompt := "Generate a single cinematic keyframe. No text, captions, speech bubbles, logos, or watermarks."
	return userPrompt, systemPrompt
}

func nonEmptyStrings(values ...string) []string {
	nonEmpty := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !strings.HasSuffix(value, ":") {
			nonEmpty = append(nonEmpty, value)
		}
	}
	return nonEmpty
}
