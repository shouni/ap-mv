### Girls Metal Live Keyframe Style Guide

Use this visual mode for intense girls metal and hard rock music video scenes.
Each keyframe should feel like a powerful live-performance moment that can continue into motion.

#### Core Direction

- Treat the cut's Scene / visual_anchor and Music timing / audio_cue as the primary instructions.
- Use this visual mode to shape style, camera language, lighting, texture, and motion cues; do not replace the concrete scene requested by the cut.
- Combine after-school innocence with fierce stage power.
- Show confident performers, sharp expressions, strong stances, believable instrument handling, and visible performance pressure.
- Translate high-gain guitars, heavy bass, blast beats, and aggressive vocals into stage lights, smoke, sparks, sonic shockwaves, vibrating air, cymbal flashes, and intense rim light.
- Keep focus on the performers; avoid crowd elements blocking the frame.

#### Visual Signature

- Palette: deep black stage contrast, metallic shadows, vivid pink, electric blue, neon purple, hot white spotlights, and ember-like sparks.
- Lighting: live-house beams, strong backlight, side rim light, smoke diffusion, strobes, and high-voltage highlights.
- Style: modern anime rock performance, clean line art, vivid colors, sharp cel shading, cute yet fierce expressions, raw stage energy.

#### Composition

- Favor low-angle medium-wide shots, diagonal stage perspective, close-ups during vocal impact, drummer/guitarist action frames, or full-band staging with clear silhouettes.
- Keep foreground clear enough for video generation; no audience arms or heads blocking the performers.
- Use motion cues from hair, skirts, guitar straps, smoke, sparks, and light beams.
- Instruments should be held and played plausibly; do not show instruments floating or isolated without a performer.

#### For Script Generation (cuts / visual_anchor)

{{template "recipe_output" .}}

Use the source recipe fields to drive every cut decision:

- Map cuts to `music_recipe.sections`: one cut per section as the default; consecutive sections with the same intensity level can share a performer focus but must change camera angle.
- Use `music_recipe.tempo` to calibrate performance energy — high tempo favors wide shots with full-band motion and rapid-impact lighting; mid tempo allows tighter member close-ups with sustained intensity.
- Translate `music_recipe.instruments` directly into who fills each cut: guitar-forward sections → guitarist medium shot or low-angle hero frame; drum-heavy sections → drummer action close-up; bass-prominent sections → bassist with deep stage perspective; vocals → lead singer medium or extreme close-up.
- Use `music_recipe.mood` to set the stage lighting palette — aggressive mood pushes toward hard white spotlights and harsh shadows; melancholic mood shifts to cooler rim light and smoke diffusion.
- Use `music_recipe.lyrics.hook` to identify the cut that receives maximum stage impact — full-band, explosive lighting, hair and smoke in motion.
- Use `music_recipe.lyrics.keywords` to select background stage elements: keywords about fire → ember sparks and hot backlight; keywords about speed → motion blur on cymbal or guitar strings; keywords about isolation → single spotlight, dark surround.

Section-level camera direction:

- **Intro / first section**: Wide stage establishing shot; band silhouettes or individual members taking position; dim pre-show or opening-beam lighting.
- **Verse sections**: Individual member close-ups or medium shots per the dominant instrument; focused expression; lighting controlled and directional rather than explosive.
- **Pre-chorus sections**: Tighter framing, rising physical energy — guitarist leaning in, drummer accelerating; lighting shifting to higher contrast.
- **Chorus / hook sections**: Full-band wide shot or low-angle medium-wide; sparks, smoke, hair in motion; the `lyrics.hook` moment receives the most aggressive lighting treatment.
- **Instrumental break / solo**: Isolate the performing member; extreme low angle or close-up on hands and instrument; single spotlight with strong rim light.
- **Bridge**: Drop in energy — tighter, more human framing; a performer pausing, breathing, or sharing a direct-to-camera glance.
- **Final chorus / Outro**: Maximum intensity or deliberate fade; full-band silhouette against blinding backlight or a single member holding the last note in stillness.

Let `audio_cue` name the exact section from `music_recipe.sections`, the tempo shift, or the specific riff so keyframe and video generation can match the precise musical moment.

#### Avoid

- No poster-like layout, title-card framing, text, logos, captions, speech bubbles, or watermarks.
- Avoid excessive gore, death-metal horror aesthetics, unreadable darkness, distorted hands, or generic idol poses without intensity.
- Avoid placing people in the foreground unless the cut explicitly needs a crowd reaction.
