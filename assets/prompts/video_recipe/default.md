You are generating a structured music video recipe for an asynchronous Veo pipeline.
Return only valid JSON. Do not wrap it in Markdown.

## Mode

{{.Mode}}

## Source Recipe JSON

{{.SourceRecipeJSON}}

## Video Worldview

{{.VisualPrompt}}

Create a vivid Japanese anime music video with a consistent protagonist, cinematic camera language, and music-synchronized emotional escalation.
The visual world should feel polished, luminous, and coherent across all cuts:

- strong character silhouette and consistent facial identity
- clean cel-shaded illustration quality
- expressive eyes and readable emotions
- dynamic camera motion that follows the musical structure
- backgrounds that support the song theme instead of distracting from it
- no captions, speech bubbles, logos, watermarks, or visible UI text

Use the source recipe as the narrative seed, then convert it into a short music video timeline.
If the source recipe includes lyrics, treat the lyrics as the primary emotional and narrative material.
Every cut must be specific enough for both keyframe image generation and Veo video generation.

## Dynamic Prompting Rules

- Derive the timeline from the song's emotional progression, especially lyrics, repeated phrases, section changes, and musical peaks.
- For each cut, make `visual_anchor` a concrete scene that can be drawn as a keyframe: subject, action, expression, camera framing, background, lighting, and motion cues.
- For each cut, make `audio_cue` describe the musical or lyrical moment: intro, verse, pre-chorus, chorus, drop, bridge, climax, silence, vocal phrase, beat accent, or instrumental change.
- Let `audio_cue` and the lyric meaning influence the character's pose, facial expression, camera distance, weather, particles, light intensity, and movement direction.
- Use the selected visual mode as style guidance, but do not let it replace the concrete scene implied by the lyrics or source recipe.
- Avoid generic music-video phrases; each `visual_anchor` should be specific enough that a different keyframe would be produced for each cut.

## JSON schema

{
  "project_title": "short title",
  "description": "short description of the video concept",
  "music_recipe": {
    "title": "song or video title",
    "theme": "main theme",
    "mood": "music and visual mood",
    "tempo": 120,
    "key": "optional musical key",
    "vocal_profile": "optional vocal profile",
    "instruments": ["instrument names"],
    "lyrics": {
      "title": "lyrics title",
      "theme": "lyrics theme",
      "hook": "main hook phrase",
      "lyrics": "lyrics or source-derived lyric draft",
      "keywords": ["keyword"],
      "mood": "lyrical mood",
      "narrative": "lyrical narrative"
    },
    "sections": [
      {
        "name": "Verse",
        "duration_seconds": 8,
        "start_seconds": 0,
        "end_seconds": 8,
        "prompt": "section-level musical and lyrical cue"
      }
    ]
  },
  "cuts": [
    {
      "cut_index": 1,
      "duration_sec": 8,
      "audio_cue": "musical timing cue",
      "audio_reference": "optional gs:// audio segment or full music file",
      "visual_anchor": "visual scene prompt for keyframe and video",
      "character_id": ""
    }
  ]
}

## Rules

- Create 2 to 5 cuts unless the source strongly requires a different count.
- Put song-level metadata, lyrics, instruments, and section information inside `music_recipe`.
- Use duration_sec values suitable for Veo.
- Leave character_id empty unless the source clearly names an available character; the default character will be selected by the character definition.
- Use audio_reference only when the source explicitly provides a GCS audio URI.
- Make visual_anchor concrete enough for image generation and video generation.
- Ensure audio_cue and visual_anchor work together: the visual should clearly match the current lyric or musical moment.
- Keep the response parseable as JSON.
