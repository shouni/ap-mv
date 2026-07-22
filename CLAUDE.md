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
3. The filter sequence for each task `command` is decided in exactly one place: `DefaultPlanner.Plan` in `internal/worker/pipeline/planner.go` (unknown commands are rejected, both there and in `domain.Task.Validate`). `pipeline.Runner` just resolves workflows (via `WorkflowResolver`, implemented by `builder.workflowResolver`), obtains the plan, and executes it; `pipeline.New(Dependencies)` validates required dependencies at construction. Filters live in `internal/worker/filter/` (`recipe_load.go`, `scripting.go`, `scene_split.go`, `cut_gen.go`, `zip_upload.go`, `video_gen.go` + `video_gen_chain.go`/`video_gen_prompt.go`, `chain_finalize.go`, `publishing.go`, `regen_cut_keyframe.go`, `section_select.go`) — file names carry no execution order; check the planner. Each filter calls into `go-veo-orchestrator/workflow` runners (Script/CutKeyframe/Video/Publish) and converts between `domain.MusicRecipe` (alias of `go-gemini-client/lyria.MusicRecipe`) and `go-veo-orchestrator/ports.VideoRecipe` via `recipe_converter.go`. Commands: `video_recipe_create` stops after keyframes + zip; `mv_from_keyframe_video_recipe` runs the full chain from an existing recipe; `short_video_from_section` generates one section only; `regenerate_cut_keyframe`/`regenerate_zip` are maintenance commands; `video_gen_continuation` is enqueued internally by `VideoGenerationFilter` to resume cut-by-cut video generation within Cloud Tasks time limits (generation is resumable per cut, not batch). Note the HTTP path names don't match the command values: the `/web/compose` route enqueues `video_recipe_create` and `/web/generate-from-recipe` enqueues `mv_from_keyframe_video_recipe`.
4. Results (including `keyframe_reference`, `video_id`, `status` per cut) are persisted as `video_music_meta.json` in GCS under `gs://<AP_MV_BUCKET>/<VEO_OUTPUT_PREFIX>/jobs/<jobID>/`, viewable via `/web/history` and `/web/history/{jobID}`.

### Key package boundaries

- `internal/ports/` — interfaces at the app's external boundaries. `VideoRunner`/`VideoGenerationRequest`/`VideoResponse` are type aliases of `go-veo-orchestrator` types, not independently defined.
- `internal/adapters/` — the only concrete implementation of `ports.VideoRunner` is `VertexVeoRunner`, which talks to Vertex AI Veo (`:predictLongRunning` + `:fetchPredictOperation` polling). No local-mode stub exists.
- `internal/builder/` — composition root: wires config into the DI container, HTTP handlers, workflow, pipeline, and prompt builder (`app.go`, `handlers.go`, `orchestrator*.go`, `pipeline.go`).
- `internal/config/` — env var loading (`caarlos0/env`) and `ValidateEssentialConfig()`.
- `internal/domain/` — task model, recipe type aliases, `job_id` validation, ASS subtitle generation.
- `internal/repository/` — reads/lists/deletes `video_music_meta.json` in GCS for the history UI; caches metadata with a short TTL but never caches signed URLs (they expire).
- `internal/server/` — `router.go` wires chi routes, OAuth, CSRF, and Cloud Tasks OIDC verification; `server.go` builds the container and runs the HTTP server with graceful shutdown.
- `internal/worker/pipeline/` and `internal/worker/filter/` — see request flow above.

### Consistency control in video generation

Veo's biggest failure mode is characters/context breaking across cuts. Four mechanisms keep cuts coherent, and touching `video_gen.go` or `VertexVeoRunner` requires understanding all four:

- **Seed-based determinism** — a per-character seed is reused across cuts.
- **Keyframe anchor (image-to-video / reference-to-video)** — a keyframe image generated from character seed + reference image anchors each cut. When the cut's character has reference art, [character art, keyframe] are sent as Veo's `referenceImages` (asset type, max 3); otherwise the keyframe is sent as the `image` input (image-to-video). `referenceImages` is only supported by non-Fast Veo 3 models (the adapter falls back to `image` otherwise), and Veo rejects `video` + `referenceImages`/`image` together, so when `VEO_USE_PREVIOUS_VIDEO` provides video-to-video context the image references are omitted for that cut.
- **Last-frame interpolation (frames-to-video)** — when a cut resolves to the `image` input path and the model supports `lastFrame` (Veo 2 / Veo 3.1 incl. Fast; not Veo 3.0), the next cut's keyframe is sent as this cut's `lastFrame` so the cut ends exactly on the next cut's opening composition. Guards in `nextCutLastFrameReference` skip it across section boundaries, across different characters, and for duration-split cuts sharing one keyframe.
- **Audio-driven prompting** — each cut's `audio_cue` (e.g. "synchronized with the heavy bass drop at 0:10") is injected into the Veo prompt.
- **Context chain (video-to-video)** — the previous cut's `VideoID` is passed as `PreviousVideoID` to the next cut's request, chaining context.

Which Veo feature a request resolves to (`video` / `referenceImages` / `image`+`lastFrame` / `image`) is decided in exactly one place — `ports.ClassifyVeoRequest` (`internal/ports/veo_mode.go`) — consumed by the adapter's request-body construction, the filter's prompt-guidance selection, and the cut-duration normalization. When adding a new Veo input mode, extend the classifier (and its capability struct `ports.VeoCapabilities`) rather than adding branches at call sites.

Cut durations treat the song timeline as the source of truth: Veo only accepts discrete durations ({4,6,8}s bases, 7s extensions), so `SceneSplitFilter` allocates each cut's length from the achievable chain lengths (`videoToVideoChainDurations`, derived from the base durations and the 24s continuation cap — do not hardcode the set) with per-cut rounding error carried into the next cut's target (error diffusion), then re-bases `StartSec`/`EndSec` onto the concatenated-video timeline so cuts never overlap. Planned chain blocks are marked `IsChainStart` and `expandCutsToSupportedDurations` realizes them as `[base, 7s, ...]` without rewriting their durations; cuts without that flag (old recipes, `SectionSelectFilter`) fall back to greedy 8s splitting + cumulative chain formation.

Resumability depends on per-cut `status`/`video_id`/`video_url` in `video_music_meta.json`: cuts already `status=generated` are skipped by `VideoTimelineRunner` and their `video_id` is reused as the next cut's `PreviousVideoID`, so re-submitting a recipe resumes rather than restarts.

### Auth model

Two independent auth paths guard `/web/*`:
- Browser sessions: Google OAuth via `gorilla/sessions`, with CSRF tokens (form field `csrf_token`, or `X-CSRF-Token` header for JS `fetch` calls like DELETE) validated against the session cookie.
- Machine-to-machine: OIDC Bearer tokens (`Authorization: Bearer <ID Token>`, audience = `SERVICE_URL`) from service accounts listed in `ALLOWED_M2M_SERVICE_ACCOUNTS`. Requests authenticated this way bypass CSRF checks and can request JSON responses via `Accept: application/json` (used by ap-mcp and other service-to-service callers). If `ALLOWED_M2M_SERVICE_ACCOUNTS` is unset, all M2M calls are rejected.

### Environment variables

The full list of runtime env vars (Veo model/output config, GCS bucket, Gemini models, Cloud Tasks queue/audience, OAuth/session secrets, Slack webhook) is documented in README.md's "Runtime Environment Variables" and "Web Security Environment Variables" tables — check there before adding or changing config rather than re-deriving it from `internal/config/config.go`.
