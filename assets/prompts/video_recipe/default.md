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

## Scene Split Strategy

Do not treat one music section as one video cut by default. A section is a musical container; split
each section into multiple directed scene beats when its duration or emotional movement needs it.
This is especially important because Veo video-to-video continuation cannot use more than about
30 seconds of previous-video context, and long single-image sections create unnatural joins.

- For each section, create 1 to 4 cuts depending on duration and musical change.
- Keep each cut at a Veo-friendly duration. For image/reference-only generation, use 4, 6, or 8
  seconds. For video-to-video continuation planning, use scene-block durations such as 8, 15, or
  22 seconds so the downstream pipeline can turn them into 8s keyframe bases followed by 7s
  continuation cuts.
- If a section is long, split it into multiple balanced scene blocks instead of making one long cut.
- Each cut inside the same section must have a distinct directorial purpose: change camera framing,
  character pose, movement direction, lighting intensity, background detail, weather/particles, or
  emotional peak.
- Consecutive cuts should connect naturally: the end pose, gaze direction, camera motion, or visual
  motif of one cut should lead into the next, while still producing a different keyframe.
- Around 24 to 30 seconds of accumulated continuation, plan a natural reset beat: a new composition,
  lighting shift, or section-aware establishing frame that can become a fresh keyframe.

## Location & Prop Continuity

Before writing any cuts, decide ONE persistent core setting for the entire video: the primary
location (e.g. "a misty coastal cliffside road overlooking the ocean at dawn, with a guardrail
along the road edge") and any persistent prop central to the concept (e.g. "her bicycle"). Write
this once into the top-level `location_anchor` field. Unless a section's narrative explicitly
moves the story to a new place, every cut happens in this same core setting.

- Every single cut's `visual_anchor` must explicitly restate this core setting (location + any
  persistent prop) in its own words, even in close-ups or emotionally-focused shots. Do not let a
  cut's `visual_anchor` omit the location or drop the persistent prop just because the camera is
  tighter or the moment is more emotional — vary the shot, not the world. `location_anchor` and
  every cut's `visual_anchor` reinforce the same setting; treat them as two independent lines of
  defense against a cut losing track of where it is, not as one being redundant with the other.
- Camera framing, character pose, lighting, weather, particles, and emotional expression should
  vary between cuts; the underlying location and persistent prop must not.
- Only change the core setting when the section's lyrics or narrative explicitly describe arriving
  somewhere new (e.g. a rooftop, a stage, a different room). When that happens, define the new
  setting just as concretely, keep it consistent for every cut that belongs to that new place, and
  set that cut's own `location_anchor` to the new setting instead of the video's default one.

## Dynamic Prompting Rules

- Derive the timeline from the song's emotional progression, especially lyrics, repeated phrases, section changes, and musical peaks.
- For each cut, make `visual_anchor` a concrete scene that can be drawn as a keyframe: subject, action, expression, camera framing, background, lighting, and motion cues.
- When several cuts belong to the same section, make each `visual_anchor` visibly different in camera framing, pose, action, lighting, or motion — but always within the same persistent core setting defined above. Do not repeat the same anchor with only minor wording changes, and never achieve "different" by omitting the location or persistent prop.
- Every `visual_anchor` must depict the single protagonist alone. Never introduce other people, crowds, band members, or background figures — downstream keyframe and video generation enforce exactly one character per cut, and an anchor describing multiple people will conflict with that constraint.
- For each cut, make `audio_cue` describe the musical or lyrical moment: intro, verse, pre-chorus, chorus, drop, bridge, climax, silence, vocal phrase, beat accent, or instrumental change.
- Let `audio_cue` and the lyric meaning influence the character's pose, facial expression, camera distance, weather, particles, light intensity, and movement direction.
- Use the selected visual mode as style guidance, but do not let it replace the concrete scene implied by the lyrics or source recipe.
- Avoid generic music-video phrases; each `visual_anchor` should be specific enough that a different keyframe would be produced for each cut.

## JSON schema

{{.OutputSchema}}

## Rules

- Create enough cuts to cover the musical sections as scene beats; 4 to 12 cuts is normal for a short MV, and longer source recipes may require more.
- Set `location_anchor` once at the top level, describing the persistent core setting (location plus any recurring prop) for the whole video; only override it on individual cuts that explicitly move to a new place.
- Put song-level metadata, lyrics, instruments, and section information inside `music_recipe`.
- Use Veo-friendly `duration_sec` values. Prefer 4, 6, or 8 seconds for ordinary image/reference cuts; use 15 or 22 seconds only when the cut is a video-to-video scene block.
- Do not output a cut longer than 22 seconds. Split the section into balanced scene blocks instead.
- Leave character_id empty unless the source clearly names an available character; the default character will be selected by the character definition.
- Use audio_reference only when the source explicitly provides a GCS audio URI.
- Make visual_anchor concrete enough for image generation and video generation.
- Ensure audio_cue and visual_anchor work together: the visual should clearly match the current lyric or musical moment.
- Keep the response parseable as JSON.
