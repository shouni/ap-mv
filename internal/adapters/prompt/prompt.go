// Package prompt builds the prompt strings sent to Gemini.
//
// There is no I/O here (apart from reading embedded assets). There are two builders, Script for
// script generation and Keyframe for keyframe generation, both satisfying go-veo-orchestrator's
// ports.ScriptPrompt / ports.KeyframePrompt. In addition, flow_music.go turns a stored recipe into
// text to paste into Google Flow Music (it is not sent; a person pastes it).
//
// It lives here rather than in internal/builder, which does DI, because it produces the prompt as
// output, not wiring of dependencies (the same role as the music-generation service's
// internal/adapters/prompt).
package prompt

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"text/template"

	characterkit "github.com/shouni/go-character-kit/character"
	promptkit "github.com/shouni/go-prompt-kit/prompts"
	"github.com/shouni/go-prompt-kit/resource"
	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"

	"github.com/shouni/ap-mv/assets"
)

const defaultPromptMode = "default"

// Script builds the prompt sent to the script-generating LLM.
// It inserts the visual mode's template into the body, so it holds two Builders, one for the
// script and one for visual styles.
type Script struct {
	builder       *promptkit.Builder
	visualBuilder *promptkit.Builder
	visualMode    string
	characters    *characterkit.Characters
	outputSchema  string
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
// video.Cut directly (no leakage issue there — it has no embedded, untagged fields), so a
// rename or removal of a Cut field breaks this build instead of silently drifting from a
// copy-pasted schema in the prompt template.
type videoRecipeSchemaExample struct {
	ProjectTitle string `json:"project_title,omitempty"`
	Description  string `json:"description,omitempty"`
	// LocationAnchor is set once for the whole video and propagated onto every cut by
	// video.Recipe.Normalize (see ports/recipe.go in go-veo-orchestrator); it grounds
	// each cut's keyframe prompt in the same persistent setting so a cut whose own VisualAnchor
	// omits the location doesn't leave the image model free to hallucinate an unrelated one.
	LocationAnchor string      `json:"location_anchor,omitempty"`
	Cuts           []video.Cut `json:"cuts"`
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
		Cuts: []video.Cut{
			{
				VisualAnchor:   "visual scene prompt for keyframe and video",
				CharacterID:    "",
				DurationSec:    8,
				AudioCue:       "musical timing cue",
				AudioReference: "optional gs:// audio segment or full music file",
			},
		},
	}
	schemaBytes, err := json.MarshalIndent(example, "", "  ")
	if err != nil {
		return "", fmt.Errorf("format recipe output schema: %w", err)
	}
	return string(schemaBytes), nil
}

// visualModeData is the flat representation of the recipe information passed to visual mode templates.
type visualModeData struct {
	Title               string
	Theme               string
	Mood                string
	Tempo               int
	Key                 string
	Instruments         []string
	Sections            []video.Section
	Hook                string
	LyricText           string
	Keywords            []string
	Narrative           string
	CharacterName       string
	CharacterVisualCues []string
}

func newVisualModeData(recipe *video.Recipe, char *characterkit.Character) visualModeData {
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
func (p *Script) defaultCharacter() *characterkit.Character {
	if p == nil || p.characters == nil {
		return nil
	}
	return p.characters.GetDefault()
}

// NewScript builds the script prompt from the embedded assets.
//
// visualMode and characters are taken as arguments because the former shape, assigning fields after
// construction, allowed a Script whose settings were forgotten to exist.
func NewScript(visualMode string, characters *characterkit.Characters) (*Script, error) {
	templates, err := loadPromptTemplates(assets.VideoRecipePrompts, assets.VideoRecipePromptDir)
	if err != nil {
		return nil, err
	}
	visualTemplates, err := assets.LoadVisualModeFiles()
	if err != nil {
		return nil, err
	}
	p, err := newScriptFromTemplates(templates, visualTemplates)
	if err != nil {
		return nil, err
	}
	p.visualMode = visualMode
	p.characters = characters
	return p, nil
}

// newScriptFromTemplates creates a script prompt from parsed templates.
func newScriptFromTemplates(templates map[string]string, visualTemplates ...map[string]string) (*Script, error) {
	// Unknown modes are delegated to the default mode. NewBuilder verifies the default mode exists.
	builder, err := promptkit.NewBuilder(templates, promptkit.WithDefaultMode(defaultPromptMode))
	if err != nil {
		return nil, fmt.Errorf("default script prompt is required: %w", err)
	}
	selectedVisualTemplates := map[string]string{}
	if len(visualTemplates) > 0 && visualTemplates[0] != nil {
		selectedVisualTemplates = visualTemplates[0]
	}
	visualBuilder, err := newVisualBuilder(selectedVisualTemplates)
	if err != nil {
		return nil, err
	}
	outputSchema, err := recipeOutputSchema()
	if err != nil {
		return nil, err
	}
	return &Script{
		builder:       builder,
		visualBuilder: visualBuilder,
		outputSchema:  outputSchema,
	}, nil
}

// newVisualBuilder creates the Builder for visual-style templates.
// Files starting with "_" are registered as partials and referenced from a mode's body with
// {{template "recipe_output" .}}.
// With no templates it returns nil, and the caller treats it as having no visual prompt.
func newVisualBuilder(visualTemplates map[string]string) (*promptkit.Builder, error) {
	if len(visualTemplates) == 0 {
		return nil, nil
	}
	builder, err := promptkit.NewBuilder(visualTemplates,
		promptkit.WithFuncs(template.FuncMap{"join": strings.Join}),
		promptkit.WithDefaultMode(defaultPromptMode),
	)
	if err != nil {
		return nil, fmt.Errorf("build visual mode templates: %w", err)
	}
	return builder, nil
}

// Build renders the script prompt for the requested mode.
func (p *Script) Build(mode string, data *orchestrator.TemplateData) (string, error) {
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
	sourceRecipeJSON, err := formatSourceRecipeJSON(data.SourceRecipe)
	if err != nil {
		return "", err
	}
	visualPrompt, err := p.visualPrompt(mode, data)
	if err != nil {
		return "", err
	}
	return p.builder.Build(mode, scriptPromptData{
		Mode:             mode,
		SourceRecipeJSON: sourceRecipeJSON,
		VisualPrompt:     visualPrompt,
		OutputSchema:     p.outputSchema,
	})
}

func formatSourceRecipeJSON(recipe *video.Recipe) (string, error) {
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

func cloneVideoRecipe(recipe *video.Recipe) (*video.Recipe, error) {
	data, err := json.Marshal(recipe)
	if err != nil {
		return nil, fmt.Errorf("clone source recipe json: %w", err)
	}
	var cloned video.Recipe
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, fmt.Errorf("clone source recipe json: %w", err)
	}
	return &cloned, nil
}

func (p *Script) visualPrompt(mode string, data *orchestrator.TemplateData) (string, error) {
	if p == nil || p.visualBuilder == nil {
		return "", nil
	}
	if p.visualMode != "" {
		mode = p.visualMode
	}
	mode = strings.TrimSpace(mode)
	if data == nil || data.SourceRecipe == nil {
		return "", nil
	}
	// Given an unknown mode or a partial name, the Builder delegates to the default mode.
	rendered, err := p.visualBuilder.Build(mode, newVisualModeData(data.SourceRecipe, p.defaultCharacter()))
	if err != nil {
		return "", fmt.Errorf("render visual mode template %q: %w", mode, err)
	}
	// Trim surrounding whitespace so the template's trailing newline is not carried into the script body.
	return strings.TrimSpace(rendered), nil
}

// loadPromptTemplates loads prompt templates. Mode names come from the file name
// without its extension.
func loadPromptTemplates(fileSystem fs.FS, rootDir string) (map[string]string, error) {
	templates, err := resource.Load(fileSystem, rootDir, resource.WithExtensions(".md"))
	if err != nil {
		return nil, fmt.Errorf("read prompt directory: %w", err)
	}
	return templates, nil
}

// Keyframe builds the prompt sent to keyframe image generation.
type Keyframe struct {
	styleSuffix     string
	visualMode      string
	visualTemplates map[string]string
}

// DefaultStyleSuffix is the default art-style instruction appended to keyframe image prompts.
// It used to be held by go-veo-orchestrator's Config, but the library never read it and only this
// package (NewKeyframe) actually used it, so it moved here, to the owner of the prompt wording.
const DefaultStyleSuffix = "Japanese anime style, official art, cel-shaded, clean line art, expressive eyes, cinematic lighting, consistent character design, high resolution"

// DefaultKeyframeNegativePrompt is the default for what to exclude from keyframe images.
//
// The wording was moved over as-is from go-veo-orchestrator. It was taken out of the kit because
// the first half (excluding speech bubbles and text) is universal for video keyframes, while the
// latter half — monochrome / black and white / greyscale — **is itself an art-style choice**. Left
// in the kit, making a monochrome MV would mean waiting for a kit release. It pairs with
// DefaultStyleSuffix, so when swapping the art style review both together (changing only one makes
// the instructions contradict each other).
const DefaultKeyframeNegativePrompt = "speech bubble, dialogue balloon, text, alphabet, letters, words, signatures, watermark, username, low quality, distorted, bad anatomy, monochrome, black and white, greyscale"

// NewKeyframe builds the keyframe prompt from the embedded assets.
func NewKeyframe(styleSuffix, visualMode string) (Keyframe, error) {
	visualTemplates, err := assets.LoadVisualModeFiles()
	if err != nil {
		return Keyframe{}, err
	}
	return Keyframe{
		styleSuffix:     styleSuffix,
		visualMode:      visualMode,
		visualTemplates: visualTemplates,
	}, nil
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
func (p Keyframe) BuildCut(cut video.Cut, char *characterkit.Character) (string, string) {
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
func (p Keyframe) BuildEdit(_ video.Cut, char *characterkit.Character, editPrompt string) (string, string) {
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

func (p Keyframe) visualPrompt() string {
	mode := strings.TrimSpace(p.visualMode)
	if mode == "" {
		mode = defaultPromptMode
	}
	if prompt := strings.TrimSpace(p.visualTemplates[mode]); prompt != "" {
		return stripScriptOnlySection(prompt)
	}
	return stripScriptOnlySection(strings.TrimSpace(p.visualTemplates[defaultPromptMode]))
}

// scriptOnlyHeading is the heading that starts the "script generation (Script) only" section in a
// visual mode file. The section holds per-section narrative direction and the Go template reference
// {{template "recipe_output" .}}, and only the Script path (newVisualBuilder) renders it.
const scriptOnlyHeading = "#### For Script Generation"

// stripScriptOnlySection removes the script-generation-only section from a visual mode template
// (from scriptOnlyHeading up to just before the next #### heading).
//
// The keyframe path uses templates raw without rendering, so the string
// {{template "recipe_output" .}} and the script-oriented direction used to be sent to the image
// model as-is. The section is cut out with string operations (rather than rendering) because the
// keyframe path has no template data to feed in.
func stripScriptOnlySection(prompt string) string {
	before, after, ok := strings.Cut(prompt, scriptOnlyHeading)
	if !ok {
		return prompt
	}
	rest := after
	if next := strings.Index(rest, "\n#### "); next != -1 {
		return strings.TrimSpace(before + rest[next+1:])
	}
	return strings.TrimSpace(before)
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
