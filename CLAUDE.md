# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

There is no Makefile; use plain Go tooling.

```bash
# Build
go build -o /app/main ./main.go

# Run all tests
go test ./...

# Run tests for one package
go test ./internal/config/...

# Run a single test by name
go test ./internal/repository/ -run TestVideoRepository_SomeCase -v

# Vet
go vet ./...

# Local run (requires env vars — see Runtime Environment section of README.md)
go run ./main.go
```

There is no local-only mode: `internal/adapters.VertexVeoRunner` talks to real Vertex AI Veo, so exercising the Veo path requires valid GCP credentials, `GCP_PROJECT_ID`/`GCP_LOCATION_ID`, and `AP_MV_BUCKET` (the GCS output bucket). `server.Run` calls `ValidateEssentialConfig()` at startup and will fail fast if required env vars (`SESSION_SECRET`, `SESSION_ENCRYPT_KEY`, OAuth, Cloud Tasks, GCS/Veo config) are missing.

Docker build is `FROM scratch` (see `Dockerfile`); `.dockerignore` must exclude `.gocache/` (local Go build cache, can exceed 1GB) or builds become extremely slow.

## Architecture

AP MV is a Cloud Run + Cloud Tasks async orchestrator that turns a "Music Recipe" (song structure JSON) into a synced music video by driving Google's Veo (Vertex AI) video generation and Gemini-based script/keyframe generation. Actual generation logic (Script/Keyframe/Video/Publish workflows) lives in the external module `github.com/shouni/go-veo-orchestrator`; this repo is the web front door, task orchestration, and adapter layer around it.

### Request flow

1. Browser submits a form to `/web/video-recipe-create` or `/web/mv-from-keyframe-video-recipe`. The handler builds a `Task` and enqueues it via Cloud Tasks (`gcp-kit/worker`), returning `202 Accepted` with a `job_id`.
2. Cloud Tasks calls back `POST /tasks/generate` with OIDC verification, which invokes `internal/worker/pipeline` to run the task through the filter chain.
3. The filter sequence for each task `command` is decided in exactly one place: `DefaultPlanner.Plan` in `internal/worker/pipeline/planner.go` (unknown commands are rejected, both there and in `domain.Task.Validate`). `pipeline.Runner` just resolves workflows (via `WorkflowResolver`, implemented by `builder.workflowResolver`), obtains the plan, and executes it; `pipeline.New(Dependencies)` validates required dependencies at construction. Filters live in `internal/worker/filter/` (`recipe_load.go`, `scripting.go`, `scene_split.go`, `cut_gen.go`, `zip_upload.go`, `video_gen.go` + `video_gen_chain.go`/`video_gen_prompt.go`, `chain_finalize.go` (concatenates chains, then probes the result via `VideoProcessor.Probe` and warns on a duration mismatch or a missing audio track — the video is still published), `publishing.go`, `regen_cut_keyframe.go`, `section_select.go`) — file names carry no execution order; check the planner. Each filter calls into `go-veo-orchestrator/workflow` runners (Script/CutKeyframe/Video/Publish) and converts between `domain.MusicRecipe` (alias of `go-gemini-client/lyria.MusicRecipe`) and `go-veo-orchestrator/ports.VideoRecipe` via `recipe_converter.go`. Commands: `video_recipe_draft` stops one step earlier than anything else — `ScriptingFilter` + `SceneSplitFilter` + `DraftSaveFilter`, writing the planned `VideoRecipe` to a **separate** prefix (`<VEO_OUTPUT_PREFIX>/drafts/<jobID>/video_recipe_draft.json`) without generating a single keyframe image, so the cut plan can be reviewed before paying for one image per cut; `video_recipe_create` stops after keyframes + zip; `mv_from_keyframe_video_recipe` runs the full chain from an existing recipe; `short_video_from_section` generates one section only; `regenerate_cut_video` redoes one cut's video: `CutVideoSelectFilter` keeps every cut in the recipe (chain finalize concatenates all of them, so trimming would yield a short instead of a repaired MV) and resets only the target cut's generation state, leaving the rest `status=generated` for `VideoTimelineRunner` to skip. Its planner chain deliberately omits `SceneSplitFilter` — re-planning a recipe whose other cuts already have videos would desync the plan from those artifacts. Under `VEO_USE_PREVIOUS_VIDEO` the reset extends to the end of the target's chain (up to the next chain base), because later cuts in a chain were generated against the target's video as `PreviousVideoID`; `chainTailEnd` finds that boundary the same way `runDirect` does (a chain base keeps its planned {4,6,8}s length, extensions are normalized to 7s). Results land in a new job, leaving the original intact. `regenerate_cut_keyframe`/`regenerate_section_keyframes`/`regenerate_zip` are maintenance commands (the two keyframe ones share a filter chain — `RegenerateCutKeyframeFilter` resolves the target from `cut_index` vs `section_index`, batching a whole section into one `RunAndSave` so its cuts are regenerated together); `video_gen_continuation` is enqueued internally by `VideoGenerationFilter` to resume cut-by-cut video generation within Cloud Tasks time limits (generation is resumable per cut, not batch). Note the HTTP path names don't match the command values: the `/web/compose` route enqueues `video_recipe_create`, `/web/compose-draft` enqueues `video_recipe_draft`, and `/web/generate-from-recipe` enqueues `mv_from_keyframe_video_recipe`.

`PUT /web/drafts/{jobID}` overwrites a draft (ap-mcp's `update_video_draft`), which is what makes the draft a review *loop* rather than a one-shot read: an agent reads the recipe, rewrites `visual_anchor`/`audio_cue`, saves, and re-reads, all without generating a single image. Note that editing cut durations in a draft is close to pointless — `SceneSplitFilter` re-allocates them from the song timeline at generation time; the fields worth editing are the prompt-side ones. Keyframe *images* are deliberately not producible from outside the pipeline: `go-veo-orchestrator/keyframe/generator.go` builds every keyframe from the character's seed plus its aspect-ratio reference art, and an externally supplied image bypasses both, which is exactly how character identity drifts across cuts.

Drafts have a list (`/web/drafts`) but deliberately **no detail page** — `GET /web/drafts/{jobID}` returns the recipe as JSON only for `Accept: application/json` (ap-mcp's `get_video_draft`) and redirects browsers back to the list. The intended reviewer of a draft's cut list is an agent over MCP, not a human reading a web page; the list carries what a human needs at a glance (cut count, section count, total duration) plus the delete and "generate from this draft" actions. Don't add a detail template without a reason that the JSON endpoint doesn't already cover.
4. Results (including `keyframe_reference`, `video_id`, `status` per cut) are persisted as `video_music_meta.json` in GCS under `gs://<AP_MV_BUCKET>/<VEO_OUTPUT_PREFIX>/jobs/<jobID>/`, viewable via `/web/history` and `/web/history/{jobID}`.

### Key package boundaries

- `internal/ports/` — interfaces at the app's external boundaries. `VideoRunner`/`VideoGenerationRequest`/`VideoResponse` are type aliases of `go-veo-orchestrator` types, not independently defined.
- `internal/adapters/` — the only concrete implementation of `ports.VideoRunner` is `VertexVeoRunner`, which talks to Vertex AI Veo (`:predictLongRunning` + `:fetchPredictOperation` polling). No local-mode stub exists.
- `internal/builder/` — composition root: wires config into the DI container, HTTP handlers, workflow, pipeline, and prompt builder (`app.go`, `handlers.go`, `orchestrator*.go`, `pipeline.go`). Which graph gets built is decided by `SERVER_ROLE` (`web` / `worker` / unset = both): `web` skips the Vertex AI client, Veo runner, Slack notifier and pipeline entirely, `worker` skips the OAuth handler and verifies Cloud Tasks OIDC with `auth.TaskVerifier` instead. Handlers the role does not serve stay nil and `router.go` guards every route group on nil, so adding a role never means touching the router — `handlers_test.go` pins which fields each role may leave nil. The Cloud Tasks enqueuer and the GCS repositories are built for both roles (the worker enqueues its own continuation cuts). See README §4.
- `internal/config/` — env var loading (`caarlos0/env`) and `ValidateEssentialConfig()`.
- `internal/domain/` — task model, recipe type aliases, `job_id` validation, ASS subtitle generation.
- `internal/repository/` — reads/lists/deletes `video_music_meta.json` in GCS for the history UI; caches metadata with a short TTL but never caches signed URLs (they expire). `draft_listing.go` does the same for `video_recipe_draft.json` under the sibling `drafts/` prefix; the two namespaces never overlap (different filename, different prefix, different job-ID prefix `video-draft-`, separate ID-list cache key), which is why a draft never shows up in the history list. The same `*VideoHistoryRepository` satisfies both `ports.HistoryRepository` and `ports.DraftRepository`.
- `internal/server/` — `router.go` wires chi routes, OAuth, CSRF, and Cloud Tasks OIDC verification; `server.go` builds the container and runs the HTTP server with graceful shutdown.
- `internal/worker/pipeline/` and `internal/worker/filter/` — see request flow above.

### Consistency control in video generation

Veo's biggest failure mode is characters/context breaking across cuts. Four mechanisms keep cuts coherent, and touching `video_gen.go` or `VertexVeoRunner` requires understanding all four:

- **Seed-based determinism** — a per-character seed is reused across cuts.
- **Keyframe anchor (image-to-video / reference-to-video)** — a keyframe image generated from character seed + reference image anchors each cut. When the cut's character has reference art, [character art, keyframe] are sent as Veo's `referenceImages` (asset type, max 3); otherwise the keyframe is sent as the `image` input (image-to-video). `referenceImages` is only supported by non-Fast Veo 3 models (the adapter falls back to `image` otherwise), and Veo rejects `video` + `referenceImages`/`image` together, so when `VEO_USE_PREVIOUS_VIDEO` provides video-to-video context the image references are omitted for that cut.
- **Last-frame interpolation (frames-to-video)** — when a cut resolves to the `image` input path and the model supports `lastFrame` (Veo 2 / Veo 3.1 incl. Fast; not Veo 3.0), the next cut's keyframe is sent as this cut's `lastFrame` so the cut ends exactly on the next cut's opening composition. Guards in `nextCutLastFrameReference` skip it across section boundaries, across different characters, and for duration-split cuts sharing one keyframe.
- **Audio-driven prompting** — each cut's `audio_cue` (e.g. "synchronized with the heavy bass drop at 0:10") is injected into the Veo prompt.
- **Context chain (video-to-video)** — the previous cut's `VideoID` is passed as `PreviousVideoID` to the next cut's request, chaining context.

Which Veo feature a request resolves to (`video` / `referenceImages` / `image`+`lastFrame` / `image`) is decided in exactly one place — and that place is **in the library**, `go-veo-orchestrator/ports/veo_mode.go`. `internal/ports/veo_mode.go` holds only type aliases and one-line delegations so ap-mv call sites can keep saying `ports.ClassifyVeoRequest`; it must never grow a second implementation. The same applies to the cut-duration tables: `veoSupportedDurationsSec` / `veoReferenceToVideoDurationsSec` / `veoVideoExtensionDurationSec` / `veoContinuationMaxDurationSec` / `videoToVideoChainDurations` in `internal/worker/filter/veo_cut_utils.go` are all thin borrows of `orchestrator.ImageToVideoDurationsSec()` / `ReferenceToVideoDurationsSec()` / `VeoVideoExtensionDurationSec` / `VeoContinuationMaxDurationSec` / `ChainDurations`. These were genuine copy-pasted duplicates until they were collapsed; the reason it matters is that the library validates every request's duration against *its* tables before sending (`runner/video.go`, `ErrUnsupportedCutDuration`), so a value changed on only one side rejects every cut. When adding a new Veo input mode or duration rule, change the library and let ap-mv follow.

`SceneSplitFilter` must stay idempotent: both `mv_from_keyframe_video_recipe` (RecipeLoad → SceneSplit over a stored recipe) and the draft flow re-run it over an already-split recipe, and a second pass that re-plans would silently change the cut plan the user just reviewed. The subtle part is `IsSectionStart`: on a fresh recipe it is set from the sub-block index (`i > 0`), but a re-split turns every already-allocated block into a single block (`i == 0`), so the scene resets would be lost — `expandCutsForVideoToVideoScenes` carries them over by reading `IsChainStart && IsSectionStart` *before* `resetCutForSceneKeyframe` clears both. `TestSceneSplitFilterIsIdempotent` and `TestDraftSaveRoundTripKeepsCutPlan` (which also covers the JSON round trip) pin this.

Cut durations treat the song timeline as the source of truth: Veo only accepts discrete durations ({4,6,8}s bases, 7s extensions), so `SceneSplitFilter` allocates each cut's length from the achievable chain lengths (`videoToVideoChainDurations`, derived from the base durations and the 24s continuation cap — do not hardcode the set) with per-cut rounding error carried into the next cut's target (error diffusion), then re-bases `StartSec`/`EndSec` onto the concatenated-video timeline so cuts never overlap. Planned chain blocks are marked `IsChainStart` and `expandCutsToSupportedDurations` realizes them as `[base, 7s, ...]` without rewriting their durations; cuts without that flag (old recipes, `SectionSelectFilter`) fall back to greedy 8s splitting + cumulative chain formation.

Keyframe images are reused rather than re-baked when a job runs again over an already-planned recipe (e.g. history detail's "generate video → full MV", which goes through `RecipeLoad → SceneSplit → CutKeyframe`). The decision itself lives in the library: `CutKeyframeRunner.RunAndSave` generates only the cuts whose `KeyframeReference` is empty. ap-mv's job is to make sure the surviving references are correct and reach it — two filters do that. `SceneSplitFilter` keeps a cut's `KeyframeReference` when the cut re-allocates to exactly one block (a re-split cut drops it, since one image can't stand for several cuts that each get their own scene beat), and `RecipeLoadFilter` absolutises job-relative references against the job the recipe came from — a relative path would otherwise satisfy the library's non-empty check while pointing at nothing under the new job's output path. To deliberately re-bake, use `regenerate_cut_keyframe` / `regenerate_section_keyframes`; picking up changed character art or seeds requires one of those, not a video re-run.

Resumability depends on per-cut `status`/`video_id`/`video_url` in `video_music_meta.json`: cuts already `status=generated` are skipped by `VideoTimelineRunner` and their `video_id` is reused as the next cut's `PreviousVideoID`, so re-submitting a recipe resumes rather than restarts.

### Auth model

Two independent auth paths guard `/web/*`:
- Browser sessions: Google OAuth via `gorilla/sessions`, with CSRF tokens (form field `csrf_token`, or `X-CSRF-Token` header for JS `fetch` calls like DELETE) validated against the session cookie.
- Machine-to-machine: OIDC Bearer tokens (`Authorization: Bearer <ID Token>`, audience = `SERVICE_URL`) from service accounts listed in `ALLOWED_M2M_SERVICE_ACCOUNTS`. Requests authenticated this way bypass CSRF checks and can request JSON responses via `Accept: application/json` (used by ap-mcp and other service-to-service callers). If `ALLOWED_M2M_SERVICE_ACCOUNTS` is unset, all M2M calls are rejected.

### Environment variables

The full list of runtime env vars (Veo model/output config, GCS bucket, Gemini models, Cloud Tasks queue/audience, OAuth/session secrets, Slack webhook) is documented in README.md's "Runtime Environment Variables" and "Web Security Environment Variables" tables — check there before adding or changing config rather than re-deriving it from `internal/config/config.go`.

## VideoRecipe validation

`domain.ValidateVideoRecipe` delegates to `orchestrator.VideoRecipe.Validate` — the rule lives with the type, in `go-veo-orchestrator/ports`. Two entry points share it:

- `VideoScriptRunner.Run` validates every AI-generated script and regenerates up to `maxScriptAttempts` times. JSON Schema constrains types and required fields only, so an empty `cuts` list or a `section_index` past the music recipe's sections slips through and would otherwise surface during Veo cut generation — the most expensive step.
- `domain.UnmarshalRecipeOrVideoRecipe` validates hand-supplied recipe JSON on the way in.

Do not re-implement the checks in ap-mv: adding one here and not there is how the two paths drifted apart before (only the hand-supplied path was validated).
