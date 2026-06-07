package builder

import (
	"fmt"
	"strings"

	orchestrator "github.com/shouni/go-veo-orchestrator/ports"
)

type scriptPrompt struct{}

func (scriptPrompt) Build(mode string, data *orchestrator.TemplateData) (string, error) {
	if data == nil || strings.TrimSpace(data.InputText) == "" {
		return "", fmt.Errorf("input text is required")
	}
	return fmt.Sprintf(`You are generating a structured music video recipe for an asynchronous Veo pipeline.
Return only valid JSON. Do not wrap it in Markdown.

Mode: %s
Source:
%s

JSON schema:
{
  "project_title": "short title",
  "title": "song or video title",
  "theme": "main theme",
  "mood": "music and visual mood",
  "tempo": 120,
  "instruments": ["instrument names"],
  "music_recipe": {
    "tempo_bpm": 120,
    "total_duration_sec": 24,
    "style": "music style"
  },
  "cuts": [
    {
      "cut_index": 1,
      "duration_sec": 8,
      "audio_cue": "musical timing cue",
      "visual_anchor": "visual scene prompt for keyframe and video",
      "character_id": "default"
    }
  ]
}

Rules:
- Create 2 to 5 cuts unless the source strongly requires a different count.
- Use duration_sec values suitable for Veo.
- Set every character_id to "default" unless the source clearly names another available character.
- Make visual_anchor concrete enough for image generation and video generation.
- Keep the response parseable as JSON.`,
		strings.TrimSpace(mode),
		strings.TrimSpace(data.InputText),
	), nil
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
