### Melancholic Hyper Techno Keyframe Style Guide

Use this visual mode for cybernetic, melancholic techno music video scenes.
Each keyframe should feel like a cinematic frame from a fast, lonely digital journey, ready to become a Veo video shot.

#### Core Direction

- Treat the cut's Scene / visual_anchor and Music timing / audio_cue as the primary instructions.
- Use this visual mode to shape style, camera language, lighting, texture, and motion cues; do not replace the concrete scene requested by the cut.
- Combine high-speed electronic motion with quiet emotional isolation.
- Translate synth arpeggios, drum machines, sub bass, and digital textures into light streaks, data fragments, hologram noise, crystalline particles, laser lines, and distorted reflections.
- Keep the character emotionally restrained but expressive: distant gaze, faint sadness, controlled posture, or a brief vulnerable moment inside a cold digital space.
- Maintain character consistency across cuts while changing camera angle, lighting density, and environmental depth.

#### Visual Signature

- Palette: deep indigo, cold black, icy crystal white, fading neon purple, blue highlights, and restrained cyan accents.
- Lighting: cold rim light, reflective neon, glitching hologram glow, passing light trails, and layered city or virtual-space depth.
- Style: modern anime with 90s cybernetic melancholy, clean line art, sharp cel shading, cinematic digital atmosphere, precise high-tech detail.

#### Composition

- Favor medium-wide rooftop shots, reflective city streets, virtual corridors, subway-like motion, close-ups through glass, or lonely silhouettes against enormous digital spaces.
- Leave readable depth for camera movement: receding neon signs, grid floors, rain reflections, screens, skyline layers, or drifting data particles.
- Use motion cues from light trails, rain, hair, coat fabric, holograms, and glitch fragments.

#### For Script Generation (cuts / visual_anchor)

{{template "recipe_output" .}}

Use the source recipe fields to drive every cut decision:

- Map cuts to `music_recipe.sections`: one cut per section as the default; the contrast between high-energy and low-energy sections is the primary structural principle — never merge a drop with a quiet section.
- Use `music_recipe.tempo` as the baseline for environment scale and motion speed — high BPM favors expansive city or virtual-space environments with fast light streaks; low BPM favors confined, reflective spaces with minimal motion.
- Translate `music_recipe.instruments` into digital visual elements: synth arpeggio → crystalline particle lattice or data stream; sub bass → low-frequency ground pulse, ripple through the floor or water reflection; drum machine → strobe-like light flicker, grid lines snapping; ambient pad → slow holographic haze or slow fog drift.
- Use `music_recipe.mood` to calibrate emotional temperature — detached or melancholic mood stays colder, darker, more isolated; tense or urgent mood increases light density and environmental scale.
- Use `music_recipe.lyrics.hook` to identify the cut where loneliness and digital beauty converge — this is the visual peak; frame the character against the largest or most luminous environment in the video.
- Use `music_recipe.lyrics.keywords` to choose specific digital environments and atmospheric details; treat them as location prompts rather than literal symbols.

Section-level camera direction:

- **Intro / first section**: Wide shot establishing the digital world; the character absent or seen only as a small silhouette; cold ambient light; deep sense of scale and loneliness before the narrative begins.
- **Verse sections**: Character moving through the environment at a measured pace; medium or medium-wide framing; restrained expression, distant gaze; light from data streams or neon casting shifting shadows keyed to the instruments in that section.
- **Drop / high-energy sections**: Energy spike — speed lines, light streaks, data fragments rushing past; camera pulls back or accelerates; character at the center of a burst of digital noise that then clears; use `music_recipe.tempo` to set the density of the streak field.
- **Chorus / hook sections**: The emotional and visual peak; character framed against an enormous environment — rooftop edge, virtual horizon, or suspended in a lattice of light; the loneliness and beauty of the `lyrics.hook` held in the same frame.
- **Bridge**: The quietest and most intimate cut; close-up or medium shot of the character pausing; the digital world slows or falls silent; a single light source, a faint reflection, a restrained glitch; the most humanly vulnerable moment in the video.
- **Outro**: Slow drift or pull-back; character becoming smaller against the environment; light trails fading; the digital space reasserting itself as the music resolves.

Let `audio_cue` name the exact section from `music_recipe.sections`, the synth phrase, or the beat change so keyframe and video generation can match the precise sonic moment.

#### Avoid

- No poster-like layout, title-card framing, text, readable signs, logos, captions, speech bubbles, or watermarks.
- Avoid over-bright pop palettes, crowded UI overlays, unreadable glitch noise, generic cyberpunk clutter, or static centered poster composition.
- Avoid exaggerated crying unless the cut explicitly asks for it; prefer subtle melancholy.
