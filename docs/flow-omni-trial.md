# Flow / Omni 1.2 Flash trial (2026-11-18)

Notes from generating a 74-second vertical music short in Google Flow, written down for ap-mv because Flow is retiring the Veo 3.1 models this service was tuned on. Everything here was observed in Flow's web UI, not through Vertex AI; the parts that need checking on Vertex are marked.

## Why this was done

Flow's model list on 2026-11-18:

| Model | Status in Flow |
| --- | --- |
| Omni 1.2 Flash | default |
| Omni 1.1 Flash | ends 2026-12-05 |
| Veo 3.1 Lite | available |
| Veo 3.1 Fast | ends 2026-12-05 |
| Veo 3.1 Quality | ends 2026-12-05 |

Flow's announcement for Omni 1.2 claims better reference adherence (character and scene), fewer jump cuts, clips up to 15 s, and extensions of up to 10 s at a time. **Not yet checked: whether Vertex AI retires the same Veo models on the same date, and whether Omni is offered on Vertex with the same request fields** (`referenceImages`, `lastFrame`, `generateAudio`, video extension). The duration tables and the 24 s chain cap in `go-veo-orchestrator` are Veo numbers and will not carry over as they are.

## Flow settings that matter

- Video settings: frames or ingredients (素材) mode, 16:9 or 9:16, model, 360p or 720p, length slider, 1-4 outputs. **There is no audio on/off switch.**
- Cost: 720p, 8 s, one output = 24 credits, in both agent mode and direct mode.
- Agent mode asks before spending credits. Direct mode spends them as soon as the prompt is sent.
- Downloads: 270p GIF, 720p original, 1080p upscale; 4K needs a higher plan. The 720p file is H.264 720x1280 at 24 fps with AAC 48 kHz stereo, 8.0 s, about 3 MB. The 1080p upscale takes about a minute per clip, and Flow asks not to run two at once; it sharpens edges a little but adds no detail.
- Uploading an image as an ingredient asks once for a usage-rights confirmation.

## Findings

### Audio cannot be silenced by prompt

| Take | Prompt | Mean / max volume |
| --- | --- | --- |
| first | "no audio" in the middle of the prompt | -39.0 / -9.1 dB (keyboard and room sounds) |
| second | led with "Silent video: no music, no sound effects, no ambient noise, no keyboard sounds, no voices" | -52.3 / -39.5 dB |

The wording removes sound *events*, but a constant noise floor remains, which is audible as hiss. For a music video the track is replaced anyway (`ffmpeg -an`, then mux the song). On Vertex, turn audio off with the request field rather than the prompt, if Omni exposes one.

### Reference images hold props across cuts

A frame cut from the first clip (desk, keyboard, cyan headphones, a small red light on the monitor) was uploaded as an ingredient for two later cuts with different camera moves and lighting (a push-in on the monitor, and a morning version of the room). Both kept the keyboard, the headphones' shape and colour, and the red light. This is the same idea as ap-mv's keyframe and reference-image anchoring: **reuse a frame of footage already generated rather than generating each cut's look from text.** It matches the chain-reset rule here, where a chain restarts from the previous chain's last frame, not from the character sheet.

### Prompt wording that was followed

- Camera moves stated as one slow, steady move ("the camera slowly tilts up from the hands toward a window") were followed in every take.
- A prop colour drifted (the "cyan" headphones came out near black) until it was restated emphatically: "bright cyan over-ear headphones, clearly cyan in color".
- A short pseudo-text appeared on a "warning" label until the prompt said "plain red light, no letters, no text".
- A list of things to avoid is not enough when the motion itself tends toward them. "Cracks spread and red light seeps out ... no sparks, no explosion" ended on a full-frame red (mean luma 30 → 78) with flying fragments. Saying where the light must stay fixed it: "a red glow shines from inside the cracks only; the plate stays in one piece; the red light never fills the frame" (luma 32 → 50, no debris). Likewise a "blinking" warning lamp from the lyrics was written as "grows slowly and steadily brighter, never blinking", and that held.
- "No flashes, no flicker, no pulsing brightness" held. Across the finished 74 s short, the largest frame-to-frame change in mean luma was 4.2 of 255, and that was inside a crossfade.

### Faceless cuts avoid the identity problem

Every cut showed hands, objects, rooms or scenery, and never a face. Character drift across cuts, the main failure mode that `video_gen.go`'s consistency controls exist for, then never comes up, and a story still reads through the props: a desk at night, the monitor, a circuit board, a city at dawn, the same desk in the morning. Worth offering as a script mode when a song does not need its character on screen.

## How the short was assembled

1. Five 8 s clips, one per section of the song, each stretched to fit its section (about 0.5x speed, so one clip covers about 16 s).
2. Cuts joined on the song's downbeats with 1.2 s crossfades. This is the same idea as `SceneSplitStep`, which allocates cut lengths from the song timeline.
3. Each clip was scaled to 1080x1920 and darkened per row (about 30-35 % at the top, 68 % behind the lyric band) so the burned-in lyrics stay readable while the footage stays visible.
4. The lyric overlay (karaoke, translation line, waveform, kick rings) was drawn over the footage by the existing lyric-video renderer, and the song was muxed in.

The first short took 6 generations (two of them prompt tests) and 144 credits, plus about 50 s of local rendering. The same workflow then made two more shorts (six cuts each, one regenerated); the same reference image kept the desk and headphones consistent between the first two shorts, and the headphones in the third.

## Open questions for ap-mv

- Do Vertex AI's Veo 3.1 models retire on 2026-12-05 as well?
- Is Omni available on Vertex AI, and with which of `referenceImages`, `image` + `lastFrame`, video extension and `generateAudio`?
- How long can an Omni extension chain run before it fails? Veo's limit was about 30 s of accumulated input, which is why the planner caps chains at 24 s.
- Does saturation drift on Omni extensions the way it did on Veo (`colorCorrectExtensionCut`)?
