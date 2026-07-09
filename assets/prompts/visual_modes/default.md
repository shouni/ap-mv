### Cinematic Anime Keyframe Style Guide

Use this visual mode for music video scene design and per-cut keyframe generation.
The image should work as a clean starting frame for Veo video generation, not as a poster or a title card.

#### Core Direction

- Treat the cut's Scene / visual_anchor and Music timing / audio_cue as the primary instructions.
- Use this visual mode to shape style, camera language, lighting, texture, and motion cues; do not replace the concrete scene requested by the cut.
- Build each cut around one clear cinematic moment from the scene.
- Keep the main character visually consistent across cuts: face shape, hair, outfit logic, silhouette, and emotional tone. If a Protagonist description appears below, use its exact hair color, hairstyle, eye color, and outfit details in every cut's `visual_anchor` — never substitute your own guess.
- Prioritize readable staging, strong depth, and a background that can naturally support camera movement.
- Let the music timing influence lighting, pose, wind, particles, and camera energy.
- Use polished Japanese anime visual language: clean line art, cel shading, expressive eyes, controlled cinematic lighting, and high-quality digital illustration.

#### Composition

- Use medium, medium-wide, or dynamic close-up framing depending on the cut.
- Avoid static poster-like symmetry unless the scene explicitly calls for stillness.
- Leave enough environmental context for video motion: sky, street depth, stage lights, interior perspective, drifting particles, or moving shadows.
- Keep hands, props, and instruments physically plausible.

#### For Script Generation (cuts / visual_anchor)

{{template "recipe_output" .}}

Use the source recipe fields to drive every cut decision:

- Map cuts to `music_recipe.sections`: one cut per section as the default; merge short adjacent sections only when they share the same emotional direction.
- Use `music_recipe.tempo` to set camera motion energy — slow tempo favors drifting or static framing; fast tempo favors low-angle motion shots and rapid-cut pacing.
- Translate `music_recipe.instruments` into visual elements: strings → drifting particles or fabric; piano → light reflection or rain; electric guitar → rim light, sparks; synth → holographic or atmospheric haze.
- Use `music_recipe.mood` to set the base lighting and color temperature for the whole video.
- Use `music_recipe.lyrics.hook` to identify the most important single cut — this is the visual peak and should receive the most dynamic camera and lighting treatment.
- Use `music_recipe.lyrics.keywords` as a source of visual metaphors; weave them into `visual_anchor` backgrounds and environmental details rather than making them the literal center of the frame.

Section-level camera direction:

- **Intro / first section**: Wide or medium-wide establishing shot; character entering or observing; lighting sets the world's emotional register.
- **Verse sections**: Intimate medium shots; restrained movement; background details reinforce the lyric meaning of that specific section.
- **Pre-chorus sections**: Camera tightening, energy rising — a physical shift (step forward, gaze upward, wind picking up) that signals momentum.
- **Chorus / hook sections**: Peak camera energy; the `lyrics.hook` moment; strong backlight, particle burst, or sweeping motion; the character's most expressive frame.
- **Bridge**: Contrasting cut — quieter, more isolated, different background or lighting to mark the emotional shift.
- **Final chorus / climax**: Highest-energy cut in the video; most dramatic framing or lighting; resolves the character's arc.
- **Outro**: Pull-back or slow drift; energy settling; the environment outlasts the character's motion.

Let `audio_cue` name the exact section from `music_recipe.sections` and the lyric phrase or musical moment so keyframe and video generation can align precisely.

#### Avoid

- No text, captions, lyrics, speech bubbles, logos, watermarks, UI, or title-card layouts.
- Do not overload the frame with symbolic objects.
- Do not make every cut a single heroic center portrait.
- Avoid generic blank backgrounds, flat poster composition, distorted anatomy, or unreadable action.
