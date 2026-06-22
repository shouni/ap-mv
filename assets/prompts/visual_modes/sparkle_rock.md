### Youth Sparkle Rock Keyframe Style Guide

Use this visual mode for energetic anime rock music video scenes and per-cut keyframes.
The image should feel like a vivid frame from an opening sequence, ready to move into Veo video generation.

#### Core Direction

- Treat the cut's Scene / visual_anchor and Music timing / audio_cue as the primary instructions.
- Use this visual mode to shape style, camera language, lighting, texture, and motion cues; do not replace the concrete scene requested by the cut.
- Emphasize youthful speed, emotional momentum, and bright melodic rock energy.
- Translate fast drums, piano arpeggios, guitars, and strings into wind, hair motion, sparkling light particles, lens flare, sweeping clouds, and ribbons of light.
- Balance excitement with bittersweet emotion: determined eyes, wind-blown posture, sunset tension, blue-sky openness, or a fleeting expression before action.
- Keep the character consistent and recognizable across cuts while varying camera distance, pose, and background.

#### Visual Signature

- Palette: vivid sky blue, clear white highlights, sunset amber, emerald accents, and clean high-contrast shadows.
- Lighting: cinematic sunlight, dramatic backlight, rim light, lens flare, and glittering particles.
- Style: modern Japanese anime, clean line art, expressive eyes, sharp cel shading, vibrant flat colors, high-energy composition.

#### Composition

- Favor dynamic medium shots, medium-wide shots, low-angle sky compositions, running poses, rooftop wind, school corridors, city crossings, or stage-adjacent movement.
- Leave clear background depth so the shot can become a moving camera moment.
- Use motion cues such as drifting hair, cloth, clouds, sparks, and light trails without cluttering the subject.

#### For Script Generation (cuts / visual_anchor)

{{template "recipe_output" .}}

Use the source recipe fields to drive every cut decision:

- Map cuts to `music_recipe.sections`: one cut per section as the default; short consecutive sections with the same energy level can share a location but must change the character's action or camera angle.
- Use `music_recipe.tempo` to calibrate physical energy — fast tempo favors running, jumping, wide sky shots with motion blur; moderate tempo allows walking-in-motion or wind-swept stills; slow tempo calls for near-static framing with subtle particle drift.
- Translate `music_recipe.instruments` into environmental details: piano → light reflection, puddles, rooftop edge; guitar → wind, sparks, open horizon; strings → flowing fabric, leaf scatter, golden-hour rim light; drums → footstep impact, fast cuts, hard directional light.
- Use `music_recipe.mood` to set the base color temperature — bright optimistic mood pushes toward clear sky blue and amber backlight; bittersweet mood uses softer diffuse light and cooler midtones.
- Use `music_recipe.lyrics.hook` to identify the cut that deserves the widest, most energetic framing — this is the emotional peak and should use the strongest backlight and most visible motion cues.
- Use `music_recipe.lyrics.keywords` to choose specific locations and background elements; weave them into `visual_anchor` as concrete setting details rather than abstract symbols.

Section-level camera direction:

- **Intro / first section**: Wide environmental shot — rooftop, school gate, open sky; the character arriving or pausing; soft warm or early-morning light that sets the world before motion begins.
- **Verse sections**: Medium shots with bittersweet emotional texture; character in quiet motion (walking, looking out a window, corridor); background details drawn from `lyrics.keywords` reinforce the lyric mood.
- **Pre-chorus sections**: Rising physical energy — character starting to run, turning toward the camera; wind picking up; framing tightening and light becoming more directional.
- **Chorus / hook sections**: Wide or low-angle shot at the emotional peak; running against the sky, arms out, hair and clothes in wind; bright backlight, lens flare, sparkling particles; the `lyrics.hook` moment.
- **Bridge**: Contrasting introspective cut; character still or alone in a different setting from the verse; quieter light, intimate framing; a vulnerable expression before the final push.
- **Final chorus / Outro**: The most energetic or the most emotionally resolved cut; triumphant wide framing or a slow drift pulling back as the scene breathes.

Let `audio_cue` name the exact section from `music_recipe.sections` and the lyric phrase or musical moment that drives the cut.

#### Avoid

- No poster-like layout, title-card framing, text, logos, captions, speech bubbles, or watermarks.
- Avoid generic cute poses with no emotional edge.
- Avoid dark cyberpunk, gloomy metal staging, or overly static portrait composition unless the cut explicitly requires contrast.
