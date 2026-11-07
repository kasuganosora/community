# Local Media Processing Implementation Plan

## Review repairs (2026-11-07)

- Renewable lease and attempt-isolated output keys. A second claim waits until this process's previous encode has stopped.
- Expired `saving` rows are claimable. Missing staging fails the row and frees the queue slot.
- One ffmpeg at a time in this process for every `ProcessFile`, including Matrix. Eight is the database admission cap, not the encoder count, and it is not shared across replicas.
- ffmpeg `-progress pipe:1` is parsed and written only while the claim token matches. The composer resumes a stored task id and renders saving and failure.
- Description updates no longer save the whole media row.
- Async Matrix hashes the stored bytes. Avatar decode checks the canvas size before `image.Decode`.
- Staging cleanup runs on the worker tick, 32 directories at a time.
- JPEG/MJPEG in this ffmpeg build reports unknown primaries, so those frames are assigned bt.709 rather than converted from an ICC profile the decoder did not read. Tagged video primaries are converted to bt.709. Alpha frames skip that filter.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Process new local uploads with saved WebP/WebM, re-encode, and privacy settings, fail closed, and run videos as durable bounded tasks.

**Architecture:** `systemsettings` holds the seven DB settings. `mediaproc.Decide` is the matrix. `mediaproc` shells out to `ffmpeg`/`ffprobe` with an argument vector and a frozen snapshot. `controllers/api/media` stages privately and is the only local ingress that publishes. A DB lease worker in `mediaproc`, started next to `userwebhook.Start`, finishes video (and v2 image/audio) tasks. The composer polls `GET /api/v1/media/:id/processing` for video.

**Tech Stack:** Go 1.27, Gin, GORM, existing `storage.Backend`, Alpine `ffmpeg`/`ffprobe`, Vue 3 composer, Prometheus counters in `metrics/metrics.go`.

## Global Constraints

- Defaults: image WebP on, video WebM on, both force-reencode on, privacy strip on, image quality 80 (40–95), video CRF 32 (20–50).
- Missing setting means the default. Saving one key must not rewrite another.
- Conversion and force-reencode and privacy-rewrite are full decode+encode. No `-c copy`. No "keep whichever is smaller".
- Identification, decode validation, resource limits, and rejection on failure are always on.
- With conversion, force-reencode, and privacy all off, a file that fully decodes is stored unchanged and the weaker guarantee is disclosed.
- No historical rewrite, no dual-file retention, no remote/federation/Matrix pipeline change.
- Staging is `uploads/.private-staging/<random>/` mode `0700`, never a public URL.
- A subprocess with `prlimit` is not a sandbox. Say that in the design and in `mediaproc` comments.
- Client errors are stable English sentences. Do not return FFmpeg stderr.
- Logs: id, stage, result, bytes, duration. No filename, path, stderr, or tag values.
- Do not deploy or merge.

Spec: `docs/superpowers/specs/2026-11-07-media-processing-design.md`.

---

### Task 1: Settings persistence and admin validation

**Files:**
- Create: `systemsettings/media_processing.go`
- Test: `systemsettings/media_processing_test.go`
- Modify: `controllers/api/admin/settings.go` (append fields on the `storage` section)
- Modify: `controllers/api/admin/settings_sections.go` (add `mediaProcessingFields`)
- Test: `controllers/api/admin/settings_test.go`

**Interfaces:**
- Produces: `systemsettings.KeyMediaImageToWebP`, `KeyMediaVideoToWebM`, `KeyMediaImageForceReencode`, `KeyMediaVideoForceReencode`, `KeyMediaStripPrivacy`, `KeyMediaImageQuality`, `KeyMediaVideoCRF`
- Produces: `systemsettings.MediaProcessing` struct and `LoadMediaProcessing() MediaProcessing` using `GetBool`/`GetInt` and the defaults below
- Consumes: `systemsettings.SaveChanges`, `validateSettingsValue`

- [x] **Step 1: Write the failing settings test**

```go
func TestMediaProcessingDefaultsAndRanges(t *testing.T) {
    got := systemsettings.LoadMediaProcessing()
    if !got.ImageToWebP || !got.VideoToWebM || !got.ImageForceReencode || !got.VideoForceReencode || !got.StripPrivacy {
        t.Fatalf("defaults on, got %+v", got)
    }
    if got.ImageQuality != 80 || got.VideoCRF != 32 {
        t.Fatalf("quality defaults, got %+v", got)
    }
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./systemsettings/ -run TestMediaProcessingDefaultsAndRanges -count=1`
Expected: FAIL, `LoadMediaProcessing` undefined.

- [x] **Step 3: Implement getters**

`LoadMediaProcessing` clamps quality to 40–95 and CRF to 20–50 if a stored value is outside the range, and uses 80 and 32 when the key is missing. Clamping a corrupt stored value is validation on read. The admin API still rejects out-of-range writes with 422 so a normal save cannot store them.

- [x] **Step 4: Admin fields**

Append `mediaProcessingFields()` to the storage section only. No `Env`, so `UpdateSettings` does not treat them as deployment-locked. Descriptions must state the matrix consequences from the spec (force-reencode applies when conversion is off; privacy off keeps metadata only for an unchanged original; a privacy rewrite does not flip force-reencode; generated files never copy GPS).

- [x] **Step 5: Admin test**

Save `image_to_webp=false` and `image_quality=80` together and assert `video_to_webm` is still the default true. `image_quality=10` and `video_crf=99` return 422. A non-boolean returns 422.

- [x] **Step 6: Run**

Run: `go test ./systemsettings/ ./controllers/api/admin/ -count=1 -timeout 180s`
Expected: PASS

---

### Task 2: Decision matrix

**Files:**
- Create: `mediaproc/decision.go`
- Test: `mediaproc/decision_test.go`

**Interfaces:**
- Consumes: `systemsettings.MediaProcessing`
- Produces:

```go
type Action string
const (
    ActionReject Action = "reject"
    ActionKeep   Action = "keep"
    ActionEncode Action = "encode"
)
type Plan struct {
    Action     Action
    OutputMIME string // empty when ActionKeep
    Reason     string // format_conversion, same_format, privacy_rewrite, validate_only, unsupported
}
func Decide(detectedMIME string, cfg systemsettings.MediaProcessing) Plan
```

- [x] **Step 1: Write table-driven tests**

Cover every row of the spec matrix for jpeg, webp, mp4, webm, and mpeg, plus `image/heic` and `application/octet-stream` rejected. Assert WebP input with conversion on still returns `ActionEncode` and `image/webp` (not keep). Assert conversion off, force off, privacy on returns `ActionEncode` with the original MIME and reason `privacy_rewrite`. Assert all three off returns `ActionKeep` and reason `validate_only`.

- [x] **Step 2: Run**

Run: `go test ./mediaproc/ -run TestDecide -count=1`
Expected: FAIL, package or `Decide` missing.

- [x] **Step 3: Implement `Decide`**

Supported sets exactly as the spec. Do not read or write the settings store inside `Decide`.

- [x] **Step 4: Run**

Run: `go test ./mediaproc/ -run TestDecide -count=1`
Expected: PASS

---

### Task 3: FFmpeg runner and fixtures

**Files:**
- Create: `mediaproc/ffmpeg.go`
- Create: `mediaproc/ffmpeg_args_test.go`
- Create: `mediaproc/ffmpeg_fixture_test.go`
- Modify: `metrics/metrics.go` (register the three metrics from the spec)

**Interfaces:**
- Produces:

```go
type Probe struct {
    Width, Height int
    Duration      float64
    FrameRate     float64
    HasAudio      bool
    HasAlpha      bool
    Animated      bool
    Tags          map[string]string
}
func ProbeFile(ctx context.Context, path string) (Probe, error)
func Encode(ctx context.Context, in, out string, plan Plan, cfg systemsettings.MediaProcessing, onProgress func(int)) error
func DecodeValidate(ctx context.Context, path string) error
func BuildEncodeArgs(in, out string, plan Plan, probe Probe, cfg systemsettings.MediaProcessing) []string
```

- [x] **Step 1: Arg test (no FFmpeg needed)**

Assert the returned slice contains `-nostdin`, `-protocol_whitelist`, `file,pipe`, `-threads`, `1`, `-map_metadata`, `-1`, never contains `-c` followed by `copy`, and maps only `0:v:0` plus `0:a:0?` for video. Image args must not map audio. WebP quality flag uses `cfg.ImageQuality`.

- [x] **Step 2: Run arg test, confirm FAIL, then implement `BuildEncodeArgs`.**

Run: `go test ./mediaproc/ -run TestBuildEncodeArgs -count=1`

- [x] **Step 3: Fixture test using the real binaries**

Skip only when `exec.LookPath("ffmpeg")` fails. Build inputs with FFmpeg itself:

- 16×16 red JPEG with `-metadata comment=SECRETCOMMENT -metadata make=CameraX -metadata location=+10.000+20.000`
- 16×8 PNG with alpha 128
- 2-frame GIF
- 64×64, 0.4s H.264/AAC MP4 with a sine and the same location metadata
- a JPEG whose bytes are cut in half
- a PNG IHDR claiming width 100000 and height 100000 (header only)

Assert: WebP/WebM outputs have no denylisted tags and do not contain `SECRETCOMMENT`; alpha PNG stays non-opaque; GIF becomes animated WebP (`nb_frames` > 1); MP4→WebM keeps an audio stream; same-format WebP→WebP changes bytes; truncated input and the oversized header return errors; orientation fixture (2×1 JPEG with EXIF orientation 6) swaps to height > width.

Record input and output sizes in the test via `t.Log` (not as a pass/fail on which is smaller).

- [x] **Step 4: Implement probe, encode, tag denylist, prlimit, process-group kill.**

`Encode` parses `-progress pipe:1` (`out_time_ms`) against `Probe.Duration`. Child limits: CPU 120s, address space 1 GiB, file size 512 MiB, 32 fds. Comment in source: this is not a sandbox.

- [x] **Step 5: Run**

Run: `go test ./mediaproc/ -count=1 -timeout 180s`
Expected: PASS, including fixture logs.

---

### Task 4: Schema and durable worker

**Files:**
- Create: `models/media_task.go`
- Modify: `models/media.go` (`ProcessState`, `ProcessProgress`, `ProcessError`, `BeforeCreate` default `ready`)
- Create: `migrate/scripts/media_processing.go`
- Modify: `migrate/scripts/script.go` (append `MediaProcessingScript{}`)
- Modify: `migrate/models_registry_test.go` (list `&models.MediaProcessTask{}`)
- Create: `mediaproc/worker.go`
- Test: `mediaproc/worker_test.go`
- Test: `migrate/scripts/media_processing_test.go`
- Modify: `cmd/community/main.go` (call `mediaproc.Start` beside `userwebhook.Start`)

**Interfaces:**
- Produces: `models.MediaProcessTask` table `media_process_tasks` with `media_id`, `account_id`, `state`, `progress`, `error_text`, `staging_path`, `output_key`, `claim_token`, `claim_expires_at`, `config_json`, `input_bytes`, `output_bytes`, `duration_ms`
- Produces: `mediaproc.Enqueue`, `Claim`, `Finalize`, `Cancel`, `ProcessNext`, `Start`

- [x] **Step 1: Migration test**

Empty SQLite, `AutoMigrate` a legacy `media_attachments` without `process_state`, run `MediaProcessingScript.Up`, assert the column and `media_process_tasks` exist, and a second `Up` is a no-op. Backfill sets empty `process_state` to `ready`.

- [x] **Step 2: Worker tests against SQLite**

Use a fake encode func injected by `mediaproc.SetEncoderForTest`.

- Claim of an expired `processing` lease succeeds once; a second claim of the same live token affects 0 rows.
- `Finalize` with the claim token moves the attachment to `ready` only when it is still `saving`.
- `Cancel` then `Finalize` leaves the attachment absent or `cancelled` and the fake output key is deleted.
- Calling `Finalize` twice does not create a second attachment.
- Config JSON on the task is the snapshot from enqueue time; changing `systemsettings` after enqueue does not change `task.ConfigJSON`.

- [x] **Step 3: Run, confirm FAIL, implement, re-run**

Run: `go test ./mediaproc/ ./migrate/scripts/ -run 'MediaProcessing|Worker|Claim|Finalize|Cancel' -count=1 -timeout 180s`
Expected: PASS

`Start` polls every second, concurrency 1, and requeues expired leases. Staging directories older than 24h with no live task are removed.

---

### Task 5: Shared local pipeline, fail closed

**Files:**
- Create: `controllers/api/media/pipeline.go`
- Modify: `controllers/api/media/store.go` (`storeMediaFile` and `StoreAccountImage`)
- Modify: `controllers/api/media/upload.go` (`Upload`, `UploadAsync`)
- Modify: `emojiasset/asset.go` (`StoreEmojiImage` processes output bytes before `Put`)
- Test: `controllers/api/media/pipeline_test.go`
- Modify existing upload tests whose fixtures are not decodable or that assert the original PNG bytes

**Interfaces:**
- Consumes: `mediaproc.Decide`, `ProbeFile`, `Encode`, `DecodeValidate`, `Enqueue`
- Produces: `stageLocal(ctx, reader, limit) (dir, path string, n int64, err error)`

- [x] **Step 1: Tests**

- Tiny valid PNG with defaults becomes `image/webp`, non-zero size, width/height set, thumbnail exists, response 200.
- Same PNG with all three image toggles and privacy off stores the same bytes and `image/png`.
- Truncated PNG returns 400 and leaves no `media_attachments` row and no object under `uploads/media`.
- Declared `image/jpeg` with PNG bytes returns 400 (existing magic check).
- Queue full (seed 8 `queued` tasks) returns 503 `upload queue is full` for a video.
- `StoreEmojiImage` of a PNG with defaults returns `image/webp`.
- Avatar path `StoreAccountImage` of a PNG returns a `/media/<id>/*.webp` URL.

- [x] **Step 2: Run the new test, confirm FAIL**

Run: `go test ./controllers/api/media/ -run 'TestPipeline|TestUpload_SuccessImage' -count=1 -timeout 180s`

- [x] **Step 3: Implement**

`storeMediaFile` no longer logs image errors and continues. Video from `Upload` waits on the task until the request context ends. `UploadAsync` inserts the task and returns 202 without a bare goroutine holding the bytes. v2 images and audio also go through the task so restart can finish them. Disk-full and over-cap copies delete the staging dir before returning.

Replace the undecodable ID3 fixture in success-path audio tests with an 8 kHz PCM WAV that FFmpeg can decode. Expectations that hard-code `image/png` and the original size move to the "all processing off" test.

- [x] **Step 4: Run**

Run: `go test ./controllers/api/media/ ./emojiasset/ -count=1 -timeout 300s`
Expected: PASS

---

### Task 6: Readiness gate, task API, federation metadata

**Files:**
- Modify: `controllers/api/status/media.go`
- Modify: `controllers/api/media/get.go`
- Modify: `controllers/api/media/init.go` `RegisterRoutes` and the v1 media group in `router.go` (around the existing `GET /media/:id`). Add `GET /media/:id/processing` on that v1 group only. v2 clients already poll `GET /api/v1/media/:id`; the new progress route is v1 as well.
- Modify: `controllers/api/media/serve.go`
- Modify: `serializer/media.go`
- Modify: `activitypub/featured.go`
- Test: `controllers/api/status/media_attach_test.go`
- Test: `controllers/api/media/processing_api_test.go`
- Test: `activitypub/status_object_test.go` or a focused new test next to featured coverage

**Interfaces:**
- Produces: `GetProcessing` JSON `{id,state,progress,error}`

- [x] **Step 1: Tests**

- `loadAttachableMedia` rejects `process_state=queued` and `failed` for the owner, and rejects another account's `ready` row.
- `GET /api/v1/media/:id/processing` without an account is 401; another account is 404; the owner of a `processing` row gets `progress`.
- `GET /api/v1/media/:id` is 206 while queued and 422 when failed.
- `Serve` of a queued row is 404.
- Outbound Note `attachment[0].mediaType` is the stored `content_type`, and `width`/`height` match the row.

- [x] **Step 2: Run, confirm FAIL, implement, re-run**

Run: `go test ./controllers/api/media/ ./controllers/api/status/ ./activitypub/ -count=1 -timeout 300s`
Expected: PASS

Empty `process_state` stays attachable so rows created before the column backfill, and tests that insert a zero value, still work. New rows get `ready` from `BeforeCreate` only after a successful sync store. Queued rows set `queued` explicitly.

---

### Task 7: Composer phases

**Files:**
- Modify: `frontend/src/domains/post/infrastructure/postApi.ts`
- Create: `frontend/src/domains/post/infrastructure/__tests__/uploadMedia.spec.ts`
- Modify: `frontend/src/shared/components/post/PostComposer.vue`
- Modify: `frontend/src/shared/components/post/__tests__/PostComposer.spec.ts`
- Modify: `frontend/src/shared/types/mastodon.ts` (`processing?` on the attachment)
- Modify: `frontend/src/shared/i18n/apiErrors.ts` and `apiError` in every file under `frontend/src/locales/`
- Modify: locale `PostComposer` keys in every locale file

**Interfaces:**
- `uploadMedia(file, description?, {signal, onProgress, onPhase})`
- `onPhase(phase: 'uploading'|'queued'|'processing'|'saving'|'ready'|'failed'|'cancelled', percent: number)`

- [x] **Step 1: Tests**

`uploadMedia.spec.ts` mocks `@/shared/api/client`. A `video/mp4` file posts to `/api/v2/media`, then polls `/api/v1/media/:id/processing` through `queued` and `processing` (percent 40) to `ready`, then GETs the attachment. An image posts to `/api/v1/media` only. Abort after the id resolves calls `DELETE`.

Composer test: video file, mock `onPhase('processing', 40)`, expect the processing-percent string, and a failed upload keeps the textarea value.

Add locale keys `serverQueued`, `processingPercent`, `uploadSaving`, `uploadCancelled`. Keep `uploadQueued` as the client-side "not started" string so the existing three-file test stays valid.

Map these API sentences in `apiErrors.ts`: `invalid or corrupt media`, `media exceeds the duration limit`, `media exceeds the pixel limit`, `upload queue is full`, `temporary storage is full`, `media processing failed`, `media processing was cancelled`, `media is not ready`.

- [x] **Step 2: Run, confirm FAIL, implement, re-run**

Run: `cd frontend && npm test -- --run src/domains/post/infrastructure/__tests__/uploadMedia.spec.ts src/shared/components/post/__tests__/PostComposer.spec.ts src/shared/i18n/__tests__/locales.spec.ts`
Expected: PASS

---

### Task 8: Image tool check and regression

**Files:**
- Modify: `Dockerfile` runtime `apk add` line to run `ffmpeg -hide_banner -version` and `ffprobe -hide_banner -version`

- [x] **Step 1: Dockerfile**

The Alpine `ffmpeg` package ships both binaries. The `RUN` must fail the build if either is missing. Do not claim the image was built unless the daemon ran.

- [x] **Step 2: Full relevant tests**

```text
go test ./systemsettings/ ./mediaproc/ ./controllers/api/media/ ./controllers/api/status/ ./controllers/api/admin/ ./emojiasset/ ./activitypub/ ./migrate/ ./models/ -count=1 -timeout 600s
cd frontend && npm test -- --run src/shared/components/post/__tests__/PostComposer.spec.ts src/domains/post/infrastructure/__tests__/uploadMedia.spec.ts src/shared/i18n/__tests__/locales.spec.ts
cd frontend && npm run build
ffmpeg -hide_banner -version && ffprobe -hide_banner -version
```

Report the exit codes. If `docker info` fails, record that the image build was not run.

- [x] **Step 3: Update the checkboxes in this file to `[x]` for steps that actually passed.**

## Self-review

- Spec matrix, ingress table, v1/v2 split, lease finalize, and frontend phases each have a task above.
- No task tells the implementer to pick a path or invent a package name.
- `Decide`, `Plan`, `Encode`, `MediaProcessTask`, and `GetProcessing` use the same names in later tasks.
- Remote inbound, Matrix, and historical files are absent from the task file lists on purpose.
