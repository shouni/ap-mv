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

	"github.com/shouni/ap-mv/assets"
)

const defaultPromptMode = "default"

type scriptPrompt struct {
	builder          *promptkit.Builder
	templates        map[string]string
	visualTemplates  map[string]string
	visualMode       string
	sharedVisualTmpl *template.Template
	characters       *characterkit.Characters
	outputSchema     string
}

type scriptPromptData struct {
	Mode             string
	SourceRecipeJSON string
	VisualPrompt     string
	OutputSchema     string
}

// videoRecipeSchemaExample mirrors the fields the script-generation LLM is actually asked to
// produce (project_title, description, location_anchor, cuts). music_recipe, final_video_url,
// and aspect_ratio are intentionally omitted: go-veo-orchestrator's VideoScriptRunner (>= v1.7.5)
// constrains the Gemini response with ports.VideoRecipeSchema, which excludes music_recipe (the
// runner always carries it over from the source recipe instead) and cut_index (computed by
// VideoRecipe.Normalize) from the grammar the model is allowed to emit. Showing them here would
// instruct the model to fill in a shape it is structurally prevented from outputting. Cuts reuses
// orchestrator.Cut directly (no leakage issue there — it has no embedded, untagged fields), so a
// rename or removal of a Cut field breaks this build instead of silently drifting from a
// copy-pasted schema in the prompt template.
type videoRecipeSchemaExample struct {
	ProjectTitle string `json:"project_title,omitempty"`
	Description  string `json:"description,omitempty"`
	// LocationAnchor is set once for the whole video and propagated onto every cut by
	// orchestrator.VideoRecipe.Normalize (see ports/recipe.go in go-veo-orchestrator); it grounds
	// each cut's keyframe prompt in the same persistent setting so a cut whose own VisualAnchor
	// omits the location doesn't leave the image model free to hallucinate an unrelated one.
	LocationAnchor string             `json:"location_anchor,omitempty"`
	Cuts           []orchestrator.Cut `json:"cuts"`
}

// recipeOutputSchema builds the example JSON schema shown to the script-generation LLM by
// marshaling a filled example instead of hand-writing a JSON block in the prompt template.
// Cut fields only meaningful after this pipeline stage (cut_index, keyframe_reference, video_id,
// status, start_sec, end_sec, is_chain_start, is_section_start) are left at their zero value; all
// of them are `omitempty` in ports.Cut, so they are dropped from the marshaled example rather
// than confusing the model into thinking it should populate them.
func recipeOutputSchema() (string, error) {
	example := videoRecipeSchemaExample{
		ProjectTitle:   "short title",
		Description:    "short description of the video concept",
		LocationAnchor: "the single persistent core setting for the whole video: location plus any recurring prop, e.g. 'a misty coastal cliffside road overlooking the ocean at dawn; her bicycle beside her'",
		Cuts: []orchestrator.Cut{
			{
				VisualAnchor: "visual scene prompt for keyframe and video",
				CharacterID:  "",
				AudioSync: orchestrator.AudioSync{
					DurationSec:    8,
					AudioCue:       "musical timing cue",
					AudioReference: "optional gs:// audio segment or full music file",
				},
			},
		},
	}
	schemaBytes, err := json.MarshalIndent(example, "", "  ")
	if err != nil {
		return "", fmt.Errorf("format recipe output schema: %w", err)
	}
	return string(schemaBytes), nil
}

// visualModeData はビジュアルモードテンプレートに渡すレシピ情報のフラット表現です。
type visualModeData struct {
	Title               string
	Theme               string
	Mood                string
	Tempo               int
	Key                 string
	Instruments         []string
	Sections            []orchestrator.Section
	Hook                string
	LyricText           string
	Keywords            []string
	Narrative           string
	CharacterName       string
	CharacterVisualCues []string
}

func newVisualModeData(recipe *orchestrator.VideoRecipe, char *characterkit.Character) visualModeData {
	if recipe == nil {
		return visualModeData{}
	}
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
	if char != nil {
		d.CharacterName = char.Name
		d.CharacterVisualCues = char.VisualCues
	}
	return d
}

// defaultCharacter resolves the character whose VisualCues should ground script generation.
// Only the default character is used here: at script-generation time the pipeline has not yet
// resolved a per-task character override (that happens later via applyTaskCharacterIDToVideoRecipe),
// so grounding on anything other than the default would require threading task state through the
// external ScriptPrompt/TemplateData contract.
func (p *scriptPrompt) defaultCharacter() *characterkit.Character {
	if p == nil || p.characters == nil {
		return nil
	}
	return p.characters.GetDefault()
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
	sharedTmpl, err := buildSharedVisualTemplate(selectedVisualTemplates)
	if err != nil {
		return nil, err
	}
	outputSchema, err := recipeOutputSchema()
	if err != nil {
		return nil, err
	}
	return &scriptPrompt{
		builder:          builder,
		templates:        templates,
		visualTemplates:  selectedVisualTemplates,
		sharedVisualTmpl: sharedTmpl,
		outputSchema:     outputSchema,
	}, nil
}

func buildSharedVisualTemplate(visualTemplates map[string]string) (*template.Template, error) {
	tmpl := template.New("").Funcs(template.FuncMap{"join": strings.Join})
	for name, content := range visualTemplates {
		if strings.HasPrefix(name, "_") {
			if _, err := tmpl.New(name).Parse(content); err != nil {
				return nil, fmt.Errorf("parse shared visual template %q: %w", name, err)
			}
		}
	}
	return tmpl, nil
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
		OutputSchema:     p.outputSchema,
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
	tmpl, err := p.sharedVisualTmpl.Clone()
	if err != nil {
		return "", fmt.Errorf("clone shared visual templates: %w", err)
	}
	if _, err := tmpl.New("main").Parse(raw); err != nil {
		return "", fmt.Errorf("parse visual mode template %q: %w", mode, err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "main", newVisualModeData(data.SourceRecipe, p.defaultCharacter())); err != nil {
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

// resolveCharacterPromptFields returns the keyframe-prompt character name (falling back to a
// generic label) and comma-joined visual cues for char. Shared by BuildCut and BuildEdit so both
// ground the image model on character identity identically.
func resolveCharacterPromptFields(char *characterkit.Character) (name, cues string) {
	name = "the main character"
	if char != nil {
		name = char.Name
	}
	if char != nil && len(char.VisualCues) > 0 {
		cues = strings.Join(char.VisualCues, ", ")
	}
	return name, cues
}

// BuildCut builds prompts for a single keyframe cut.
func (p keyframePrompt) BuildCut(cut orchestrator.Cut, char *characterkit.Character) (string, string) {
	character, cues := resolveCharacterPromptFields(char)
	userPrompt := strings.Join(nonEmptyStrings(
		fmt.Sprintf("Create a clean keyframe image for cut %d.", cut.CutIndex),
		"Character: "+character,
		"Character visual cues: "+cues,
		"Persistent location & recurring prop (keep this exact setting in every cut unless the scene below explicitly describes arriving somewhere new): "+strings.TrimSpace(cut.LocationAnchor),
		"Scene: "+strings.TrimSpace(cut.VisualAnchor),
		"Music timing: "+strings.TrimSpace(cut.AudioCue),
		p.visualPrompt(),
		strings.TrimSpace(p.styleSuffix),
	), "\n")
	systemPrompt := "Generate a single cinematic keyframe depicting exactly one character. " +
		"The reference image may show that same character from multiple angles (e.g. front/side/back " +
		"turnaround) for identity and art-style reference only — treat it as one character's design " +
		"sheet, not multiple people, and never depict more than one character or reproduce that " +
		"multi-view layout in the output. No text, captions, speech bubbles, logos, or watermarks."
	return userPrompt, systemPrompt
}

// BuildEdit builds prompts for editing an existing keyframe image with editPrompt, reinforcing
// character identity and art style the same way BuildCut does so the edited result doesn't
// drift from the rest of the cuts.
func (p keyframePrompt) BuildEdit(_ orchestrator.Cut, char *characterkit.Character, editPrompt string) (string, string) {
	character, cues := resolveCharacterPromptFields(char)
	userPrompt := strings.Join(nonEmptyStrings(
		"Edit the provided keyframe image. Keep the composition, pose, background, and art style exactly as they are; apply only this change.",
		strings.TrimSpace(editPrompt),
		"Character: "+character,
		"Character visual cues: "+cues,
		strings.TrimSpace(p.styleSuffix),
	), "\n")
	systemPrompt := "Edit the provided keyframe image with minimal changes beyond the requested edit. No text, captions, speech bubbles, logos, or watermarks."
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
