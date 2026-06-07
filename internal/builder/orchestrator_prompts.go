package builder

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	promptkit "github.com/shouni/go-prompt-kit/prompts"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"ap-mv/assets"
)

const defaultPromptMode = "default"

type scriptPrompt struct {
	builder   *promptkit.Builder
	templates map[string]string
}

type scriptPromptData struct {
	Mode      string
	InputText string
}

func newScriptPrompt() (*scriptPrompt, error) {
	templates, err := loadPromptTemplates(assets.Prompts, assets.PromptDir)
	if err != nil {
		return nil, err
	}
	return newScriptPromptFromTemplates(templates)
}

func newScriptPromptFromTemplates(templates map[string]string) (*scriptPrompt, error) {
	builder, err := promptkit.NewBuilder(templates)
	if err != nil {
		return nil, err
	}
	if _, ok := templates[defaultPromptMode]; !ok {
		return nil, fmt.Errorf("default script prompt is required")
	}
	return &scriptPrompt{builder: builder, templates: templates}, nil
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
	templateMode := mode
	if _, ok := p.templates[templateMode]; !ok {
		templateMode = defaultPromptMode
	}
	return p.builder.Build(templateMode, scriptPromptData{
		Mode:      mode,
		InputText: strings.TrimSpace(data.InputText),
	})
}

func loadPromptTemplates(fileSystem fs.FS, rootDir string) (map[string]string, error) {
	entries, err := fs.ReadDir(fileSystem, rootDir)
	if err != nil {
		return nil, fmt.Errorf("read prompt directory: %w", err)
	}

	templates := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".md" {
			continue
		}
		mode := strings.TrimSuffix(entry.Name(), path.Ext(entry.Name()))
		content, err := fs.ReadFile(fileSystem, path.Join(rootDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read script prompt %s: %w", entry.Name(), err)
		}
		templates[mode] = string(content)
	}
	return templates, nil
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
