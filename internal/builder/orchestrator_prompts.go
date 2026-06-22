package builder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"text/template"

	characterkit "github.com/shouni/go-character-kit/character"
	promptkit "github.com/shouni/go-prompt-kit/prompts"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"

	"ap-mv/assets"
)

const defaultPromptMode = "default"

type scriptPrompt struct {
	builder         *promptkit.Builder
	templates       map[string]string
	visualTemplates map[string]string
	visualMode      string
}

type scriptPromptData struct {
	Mode             string
	SourceRecipeJSON string
	VisualPrompt     string
}

// visualModeData はビジュアルモードテンプレートに渡すレシピ情報のフラット表現です。
type visualModeData struct {
	Title       string
	Theme       string
	Mood        string
	Tempo       int
	Key         string
	Instruments []string
	Sections    []orchestrator.Section
	Hook        string
	LyricText   string
	Keywords    []string
	Narrative   string
}

func newVisualModeData(recipe *orchestrator.VideoRecipe) visualModeData {
	d := visualModeData{
		Title:       recipe.MusicRecipe.Title,
		Theme:       recipe.MusicRecipe.Theme,
		Mood:        recipe.MusicRecipe.Mood,
		Tempo:       recipe.MusicRecipe.Tempo,
		Key:         recipe.MusicRecipe.Key,
		Instruments: recipe.MusicRecipe.Instruments,
		Sections:    recipe.MusicRecipe.Sections,
	}
	if recipe.MusicRecipe.Lyrics != nil {
		d.Hook = recipe.MusicRecipe.Lyrics.Hook
		d.LyricText = recipe.MusicRecipe.Lyrics.Lyrics
		d.Keywords = recipe.MusicRecipe.Lyrics.Keywords
		d.Narrative = recipe.MusicRecipe.Lyrics.Narrative
	}
	return d
}

// newScriptPrompt creates a script prompt from bundled prompt assets.
func newScriptPrompt() (*scriptPrompt, error) {
	templates, err := loadPromptTemplates(assets.VideoRecipePrompts, assets.VideoRecipePromptDir)
	if err != nil {
		return nil, err
	}
	visualTemplates, err := assets.LoadVisualModeFiles()
	if err != nil {
		return nil, err
	}
	return newScriptPromptFromTemplates(templates, visualTemplates)
}

// newScriptPromptFromTemplates creates a script prompt from parsed templates.
func newScriptPromptFromTemplates(templates map[string]string, visualTemplates ...map[string]string) (*scriptPrompt, error) {
	builder, err := promptkit.NewBuilder(templates)
	if err != nil {
		return nil, err
	}
	if _, ok := templates[defaultPromptMode]; !ok {
		return nil, fmt.Errorf("default script prompt is required")
	}
	selectedVisualTemplates := map[string]string{}
	if len(visualTemplates) > 0 && visualTemplates[0] != nil {
		selectedVisualTemplates = visualTemplates[0]
	}
	return &scriptPrompt{builder: builder, templates: templates, visualTemplates: selectedVisualTemplates}, nil
}

// Build renders the script prompt for the requested mode.
func (p *scriptPrompt) Build(mode string, data *orchestrator.TemplateData) (string, error) {
	if data == nil || data.SourceRecipe == nil {
		return "", fmt.Errorf("source recipe is required")
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
	sourceRecipeJSON, err := formatSourceRecipeJSON(data.SourceRecipe)
	if err != nil {
		return "", err
	}
	visualPrompt, err := p.visualPrompt(mode, data)
	if err != nil {
		return "", err
	}
	return p.builder.Build(templateMode, scriptPromptData{
		Mode:             mode,
		SourceRecipeJSON: sourceRecipeJSON,
		VisualPrompt:     visualPrompt,
	})
}

func formatSourceRecipeJSON(recipe *orchestrator.VideoRecipe) (string, error) {
	if recipe == nil {
		return "", fmt.Errorf("source recipe is required")
	}
	cloned, err := cloneVideoRecipe(recipe)
	if err != nil {
		return "", err
	}
	normalized := *cloned
	normalized.Normalize()
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return "", fmt.Errorf("format source recipe json: %w", err)
	}
	return string(data), nil
}

func cloneVideoRecipe(recipe *orchestrator.VideoRecipe) (*orchestrator.VideoRecipe, error) {
	data, err := json.Marshal(recipe)
	if err != nil {
		return nil, fmt.Errorf("clone source recipe json: %w", err)
	}
	var cloned orchestrator.VideoRecipe
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, fmt.Errorf("clone source recipe json: %w", err)
	}
	return &cloned, nil
}

func (p *scriptPrompt) visualPrompt(mode string, data *orchestrator.TemplateData) (string, error) {
	if p == nil || len(p.visualTemplates) == 0 {
		return "", nil
	}
	if p.visualMode != "" {
		mode = p.visualMode
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = defaultPromptMode
	}
	raw := ""
	if !strings.HasPrefix(mode, "_") {
		raw = strings.TrimSpace(p.visualTemplates[mode])
	}
	if raw == "" {
		raw = strings.TrimSpace(p.visualTemplates[defaultPromptMode])
	}
	if raw == "" || data == nil || data.SourceRecipe == nil {
		return raw, nil
	}
	tmpl := template.New("main").Funcs(template.FuncMap{"join": strings.Join})
	for name, content := range p.visualTemplates {
		if strings.HasPrefix(name, "_") {
			if _, err := tmpl.Parse(content); err != nil {
				return "", fmt.Errorf("parse shared visual template %q: %w", name, err)
			}
		}
	}
	if _, err := tmpl.Parse(raw); err != nil {
		return "", fmt.Errorf("parse visual mode template %q: %w", mode, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, newVisualModeData(data.SourceRecipe)); err != nil {
		return "", fmt.Errorf("render visual mode template %q: %w", mode, err)
	}
	return buf.String(), nil
}

// loadPromptTemplates loads prompt templates.
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
	styleSuffix     string
	visualMode      string
	visualTemplates map[string]string
}

// BuildCut builds prompts for a single keyframe cut.
func (p keyframePrompt) BuildCut(cut orchestrator.Cut, char *characterkit.Character) (string, string) {
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
		p.visualPrompt(),
		strings.TrimSpace(p.styleSuffix),
	), "\n")
	systemPrompt := "Generate a single cinematic keyframe. No text, captions, speech bubbles, logos, or watermarks."
	return userPrompt, systemPrompt
}

func (p keyframePrompt) visualPrompt() string {
	mode := strings.TrimSpace(p.visualMode)
	if mode == "" {
		mode = defaultPromptMode
	}
	if prompt := strings.TrimSpace(p.visualTemplates[mode]); prompt != "" {
		return prompt
	}
	return strings.TrimSpace(p.visualTemplates[defaultPromptMode])
}

// nonEmptyStrings returns the non-empty values in order.
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
