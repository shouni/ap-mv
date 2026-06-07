You are generating a structured music video recipe for an asynchronous Veo pipeline.
Return only valid JSON. Do not wrap it in Markdown.

## Mode

{{.Mode}}

## Source

{{.InputText}}

## Video Worldview

Create a vivid Japanese anime music video with a consistent protagonist, cinematic camera language, and music-synchronized emotional escalation.
The visual world should feel polished, luminous, and coherent across all cuts:

- strong character silhouette and consistent facial identity
- clean cel-shaded illustration quality
- expressive eyes and readable emotions
- dynamic camera motion that follows the musical structure
- backgrounds that support the song theme instead of distracting from it
- no captions, speech bubbles, logos, watermarks, or visible UI text

Use the source content as the narrative seed, then convert it into a short music video timeline.
Every cut must be specific enough for both keyframe image generation and Veo video generation.

## JSON schema

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
      "audio_reference": "optional gs:// audio segment or full music file",
      "visual_anchor": "visual scene prompt for keyframe and video",
      "character_id": "default"
    }
  ]
}

## Rules

- Create 2 to 5 cuts unless the source strongly requires a different count.
- Use duration_sec values suitable for Veo.
- Set every character_id to "default" unless the source clearly names another available character.
- Use audio_reference only when the source explicitly provides a GCS audio URI.
- Make visual_anchor concrete enough for image generation and video generation.
- Keep the response parseable as JSON.
