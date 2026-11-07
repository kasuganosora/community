# Local media processing

## Goal

New local uploads are identified, decoded, and, when the saved policy says so, fully re-encoded before anything is public or attachable. Images can become WebP and videos WebM. Privacy metadata is stripped by default. Video work is a durable, bounded background task. Any identification, decode, transcode, or output-validation failure rejects the attachment. The original file is never published as a fallback.

## Non-goals

- No rewrite of files already stored.
- No second file kept for old browsers, and no compatibility remux.
- Remote cache, media-proxy fetches, and ActivityPub inbound attachments stay on their current path (`activitypub/attachments.go` stores `remote_url` and does not download the bytes).
- Matrix federation copies and link-preview fetches stored with `origin=remote` are not transcoded. Local Matrix uploads are in scope.
- Transcoding is not a security boundary. A successful encode does not mean the decoder was safe.

## Current architecture

| Concern | Where it lives today |
|---|---|
| Sync upload | `POST /api/v1/media` → `controllers/api/media/upload.go` `Upload` |
| Async upload | `POST /api/v2/media` and `POST /api/v1/media/v2` → `UploadAsync`. The handler keeps the bytes in memory and finishes in a goroutine (`processUploadAsync`). A restart drops that work. Failure deletes the row. |
| Shared store | `controllers/api/media/store.go` `storeMediaFile` writes a temp file, best-effort thumbnail, then `storage.PutFile`. Image-processing errors are logged and the original is still stored. |
| Avatar and header | `StoreAccountImage` in the same file, called from `controllers/api/account/update.go`. |
| Custom emoji | `emojiasset.StoreEmojiImage`, called from `controllers/api/admin/emojis.go` and `community_assets.go`. It validates with `DecodeImage` and stores the original bytes. |
| Attachment gate | `controllers/api/status/media.go` `loadAttachableMediaDB` checks owner and that the row is not linked to another status. It does not check readiness. |
| Read API | `controllers/api/media/get.go` `Get` is owner-only. `Serve` refuses `file_name == "processing"`. |
| Public URL | `serializer/media.go` `ToMediaAttachment`. Federation copies `content_type` and `/media/<id>/<file>` in `activitypub/featured.go`. |
| Admin settings | `controllers/api/admin/settings.go` section `storage`, fields from `settings_sections.go` `storageSettingsFields`. Values go through `systemsettings.SaveChanges`. `.env` overrides a key only when that key is present in `config.C.Other`. |
| Background work | In-process loops with a DB lease, started from `cmd/community/main.go`. `userwebhook.Start` is the pattern: claim token, conditional update, restart resumes expired leases. |
| Production image | `Dockerfile` runtime stage already installs Alpine `ffmpeg` (that package provides both `ffmpeg` and `ffprobe`). The build does not fail if `ffprobe` is absent. |
| Web composer | `frontend/src/domains/post/infrastructure/postApi.ts` `uploadMedia` always uses synchronous `/api/v1/media`. `PostComposer.vue` shows client-side queued / uploading percent / processing / complete. |

`models.ThumbnailJob` is a separate, URL-based thumbnail queue. It is not used for uploads and is not reused. Staging must not be a public URL.

## Defaults

Stored in `system_settings`. Missing keys use these defaults. Saving one value does not rewrite the others.

| Key | Default | Range |
|---|---|---|
| `config.media_processing.image_to_webp` | true | boolean |
| `config.media_processing.video_to_webm` | true | boolean |
| `config.media_processing.image_force_reencode` | true | boolean |
| `config.media_processing.video_force_reencode` | true | boolean |
| `config.media_processing.strip_privacy` | true | boolean |
| `config.media_processing.image_quality` | 80 | integer 40–95 |
| `config.media_processing.video_crf` | 32 | integer 20–50 |

`video_crf` is passed to libvpx-vp9 for WebM and to libx264 for same-format MP4/MOV re-encodes. Higher means smaller. Image quality is the WebP/JPEG quality (higher means larger).

These are not optional, and there is no switch that turns them off:

| Limit | Value | Source |
|---|---|---|
| Image byte cap | existing `config.media_attachments.image_size_limit` | `systemsettings.MediaImageSizeLimit` |
| Video/audio byte cap | existing `config.media_attachments.video_size_limit` | `systemsettings.MediaVideoSizeLimit` |
| Image pixels | existing `config.media_attachments.image_matrix_limit`, hard cap 32,000,000 | already capped in `validateFileUpload` |
| Video pixels | existing `config.media_attachments.video_matrix_limit` (default 8,294,400) | |
| Video frame rate | existing `config.media_attachments.video_frame_rate_limit` (default 120) | |
| Video duration | 300 seconds | constant |
| Video workers | 1 | constant |
| FFmpeg threads | 1 | argument |
| Image FFmpeg timeout | 60 seconds | constant |
| Video task timeout | 3 minutes | constant |
| Lease | 2 minutes, refreshed while running | constant |
| Per-user queued+active videos | 2 | constant |
| Global queued+active videos | 8 | constant |
| Free disk required | max(256 MiB, 3× input + 64 MiB) | `statfs` on the staging directory; statfs failure rejects |
| Output byte cap | the same image or video limit as the input | measured after encode |

CPU cost is bounded by the worker count, thread count, duration, pixel caps, and timeout. Leaving WebM conversion off does not avoid a re-encode when force-reencode or privacy rewrite is on.

## Processing matrix

`Decide` looks only at the detected type and the snapshot. It never writes settings back.

Supported images: `image/jpeg`, `image/png`, `image/gif`, `image/webp`.
Supported videos: `video/mp4`, `video/webm`, `video/quicktime`.
Supported audio: `audio/mpeg`, `audio/ogg`, `audio/wav`.
Anything else is rejected. HEIC, AVIF, BMP, and subtitle/data-only files are unsupported. Extra audio or subtitle tracks are not copied; video keeps at most the first video stream and the first audio stream.

| Conversion | Force re-encode | Privacy | Result |
|---|---|---|---|
| on | stored, ignored for the output format | on or off | Full decode and encode to WebP or WebM, including when the input is already that format. Never stream-copy. |
| off | on | on or off | Full decode and encode to the same supported MIME. |
| off | off | on | Same-format privacy rewrite (still a full decode and encode). This does not flip the force-reencode setting. |
| off | off | off | Decode-validate the original. If that succeeds, store those exact bytes. Metadata is not removed. This is the weaker guarantee and the admin copy says so. |

Generated files use a metadata allowlist. `-map_metadata -1` and bitexact flags are always set on an encode. Closing the privacy toggle does not copy GPS or device tags into a generated file. The toggle being off only skips the rewrite in the last row. If a required rewrite cannot be produced and validated, the upload is rejected.

Orientation is applied by FFmpeg's default autorotate before tags are dropped. Opaque images use a 4:2:0 pixel format; images with alpha use a format that keeps alpha. Animation is preserved for GIF and animated WebP (`-loop 0`). Video audio, when a stream exists, is encoded to Opus inside WebM or to AAC inside same-format MP4/MOV. A video whose audio stream cannot be encoded is rejected rather than published silent.

"Only keep the output when it is smaller" is not used. The validated output is the only publishable file whenever a rewrite was required.

## Pipeline

1. Stream the body to `uploads/.private-staging/<random>/input` with mode `0700`, enforcing the byte cap while copying. The directory is on the persisted `./uploads` volume (`docker-compose.yml`). It is not a storage key. `CleanKey` rejects that prefix, object listing skips it, and `Open`/`Stat` refuse a symlink whose target is that directory or is outside the storage root. `Serve` only opens the attachment's own file name or thumbnail.
2. Detect type from bytes (`filetype` plus, for video and audio, `ffprobe`). The filename and the client `Content-Type` are not trusted. A declared type that disagrees with the bytes is rejected (this already happens in `validateMagicBytes`).
3. Enforce pixel, duration, and frame-rate limits from container metadata before FFmpeg. Image uploads still pass `emojiasset.DecodeImage` inside `validateFileUpload` before this step.
4. Run the matrix. `POST /api/v1/media` images and audio, avatars, headers, and emoji run in the request and return only when ready. `POST /api/v1/media` video enqueues and blocks on the request context until ready or failed. `POST /api/v2/media` and `POST /api/v1/media/v2` enqueue every accepted type and return 202; the worker finishes the snapshot, so a restart can recover an image as well as a video.
5. Validate the output the same way: magic, `ffprobe`, dimensions, duration, size, and, when an encode ran, an allowlist of tags. Allowed names are `handler_name`, `vendor_id`, `major_brand`, `compatible_brands`, `encoder` only when the value starts with `Lavf` or `Lavc`, and `duration` only when the value is a number or `hh:mm:ss` timecode. Any other tag rejects the file.
6. `storage.Put` the output, then the thumbnail. Image thumbnails still come from `media.ImageProcessor` on the output pixels (a new JPEG, so the thumbnail has no EXIF). Video gets a one-frame JPEG poster. Thumbnail failure rejects the upload.
7. Set the attachment row to `ready` with the output MIME, extension, byte size, dimensions, duration, and thumbnail name. Only then delete the staging input.
8. On any failure: delete staging and any uncommitted output. Do not leave a public object and do not mark the row ready.

Avatars and headers stay synchronous through `StoreAccountImage`. Custom emoji and the brand images that share `StoreEmojiImage` use the same image decision, then the existing preview PNG is decoded from the output. Emoji limits stay 2 MiB, 1024 px, 128 frames. Remote emoji that only stores a hotlink (`activitypub/emoji.go`) is unchanged. `ImportEmojiImage` downloads and then calls `StoreEmojiImage`, so the stored local copy is processed; the fetch itself is unchanged.

Audio has no format-conversion toggle. Privacy on rewrites it in the same format. Privacy off still requires a successful full decode before the original bytes are stored.

## Video tasks

`media_process_tasks` plus `media_attachments.process_state`:

`queued`, `processing`, `saving`, `ready`, `failed`, `cancelled`.

The task row freezes a JSON snapshot of the seven settings and the limits above. The worker uses the snapshot, not the live settings.

Claim matches `userwebhook`: update `queued` or an expired `processing` lease to `processing` with a new token. Restart does not run the encode twice to completion. Finalize is one transaction:

```text
UPDATE media_process_tasks
  SET state = 'ready'
  WHERE id = ? AND claim_token = ? AND state = 'saving'
```

and the same token must still see the attachment in `saving`. Zero rows means the upload was deleted or cancelled. The worker deletes the object it just wrote and does not insert another attachment.

`DELETE /api/v1/media/:id` sets `cancelled` before removing files. A finalize that loses the race cannot flip the row back to `ready`.

Orphan staging directories older than 24 hours are removed on the worker loop. Terminal tasks delete their staging directory immediately.

Queue or disk overflow returns HTTP 503 with `upload queue is full` or `temporary storage is full` and leaves no public object.

## API

- `POST /api/v1/media` for images and audio stays synchronous and returns 200 only when `ready`. For video it enqueues, waits on the request context until `ready` or `failed`, and returns 200 or 422. A disconnect does not cancel the task by itself.
- `POST /api/v2/media` and `POST /api/v1/media/v2` enqueue video immediately and return 202 with an empty `url`, as they do today. Images and audio on v2 still finish in the durable worker so a restart can recover them too; the 202 body stays the Mastodon shape.
- `GET /api/v1/media/:id` stays owner-only. `206` while `queued|processing|saving` (empty `url`). `200` when `ready`. `422` when `failed` or `cancelled`. Another account gets `404`. No account gets `401`.
- `GET /api/v1/media/:id/processing` is owner-only and returns `{id, state, progress, error}`. The error is a stable English sentence, not FFmpeg stderr.
- `loadAttachableMediaDB` requires owner and `process_state` in `""` or `ready`. The web composer waiting for an id is not the enforcement.

`Serve` returns 404 unless `process_state` is `ready` or empty (rows from before the migration).

Federation `Document` objects in `activitypub/featured.go` take `mediaType`, filename URL, width, height, and duration from the ready row. Inbound remote documents are not re-encoded.

## Admin copy

The fields are added to the existing storage section so they persist through `SaveChanges` and are not deployment-locked (no `deployment_key`). Help text states:

- Force-reencode applies when conversion is off. Conversion already re-encodes, and saving conversion does not change the stored force-reencode value.
- Turning privacy off keeps metadata only when the file is published unchanged. Any generated file still drops tags outside the allowlist.
- Turning privacy on while conversion and force-reencode are off still rewrites the same format and does not turn force-reencode on.
- A failed rewrite is rejected. Disabling WebM does not remove re-encode cost when another rewrite is required.

## Frontend

`uploadMedia` keeps `/api/v1/media` for non-video files. Video (type or extension `mp4`, `webm`, `mov`, `m4v`) uses `/api/v2/media` and polls `/processing`. The composer label distinguishes:

| Phase | Meaning |
|---|---|
| client queue | not started (`待上传` / `Queued`) |
| uploading | XHR percent |
| queued | accepted, waiting for the worker |
| processing | FFmpeg percent from `out_time_ms / duration` |
| saving | output being stored |
| ready | attachment returned |
| failed / cancelled | draft text kept, retry uploads again, remove calls `DELETE` when an id exists |

Abort after an id is known sends `DELETE` so a later finalize cannot attach the file. Posting still sends `media_ids` only for ready attachments.

## Isolation

FFmpeg and ffprobe are executed by `prlimit`, which sets `RLIMIT_CPU`, `RLIMIT_AS` (1 GiB), `RLIMIT_FSIZE`, and `RLIMIT_NOFILE` and then execs the tool. Parsing therefore starts only after those caps exist. Arguments are a vector, not a shell. `-protocol_whitelist file,pipe` is set before `-i`, and network plus external-reference protocols (`http`, `https`, `tcp`, `tls`, `concat`, `data`, `subfile`, and the rest of the blacklist) are denied. The child environment is only `LANG` and `LC_ALL`, so a proxy variable is not inherited. The child is its own process group and is killed when the timeout fires or the parent thread dies.

This is still not an OS sandbox. There is no seccomp, user namespace, or network namespace. A hostile decoder can use CPU until the timeout. `prlimit` must be installed; without it processing fails closed instead of starting the parser unlimited.

Local Matrix uploads (`UploadMedia`, async completion, and web-group uploads, all `origin=local`) go through the same image/video/audio gate. Bytes that are not image, video, or audio stay opaque files. Claimed image, video, or audio whose bytes do not match is rejected. Federation copies stored with `origin=remote` are not transcoded.

Staging is a private directory on the server disk. Do not expose `/app/uploads` with a file server. A public object-storage bucket is not used for staging. Final objects become reachable only after the row is `ready`, through `/media/<id>/...`.

## Ingress

| Entry | In this change |
|---|---|
| `POST /api/v1/media`, `/api/v1/media/v2`, `/api/v2/media` | Yes |
| Avatar and header multipart (`StoreAccountImage`) | Yes, images only |
| Profile crop, which uploads via `postApi.uploadMedia` | Yes, because that is `/api/v1/media` |
| Custom emoji and brand images (`StoreEmojiImage`) | Yes |
| Chat composer, which uses `uploadMedia` | Yes |
| ActivityPub inbound attachments | No. Remote URL only. |
| `mediaproxy` and remote emoji hotlinks | No |
| Matrix local upload, async completion, and web-group upload (`origin=local`) | Yes, for sniffed image, video, and audio. Other bytes stay opaque. A claimed image, video, or audio type whose bytes do not match is rejected. |
| Matrix federation inbound and link-preview fetch (`origin=remote`) | No |

## Observability

Prometheus counters on the existing registry in `metrics/metrics.go`:

- `community_media_processing_total{kind,result}`
- `community_media_processing_bytes_total{direction}` (`in` or `out`)
- `community_media_processing_duration_seconds`

Logs include the attachment id, stage, result, byte counts, and duration. They omit filenames, staging paths, FFmpeg stderr, and tag values.

## Tests

Commands that must be run and reported with their real output:

```text
go test ./systemsettings/ ./mediaproc/ ./controllers/api/media/ ./controllers/api/status/ ./controllers/api/admin/ ./emojiasset/ ./activitypub/ -count=1
cd frontend && npm test -- --run src/shared/components/post/__tests__/PostComposer.spec.ts src/domains/post/infrastructure/__tests__/uploadMedia.spec.ts src/shared/i18n/__tests__/locales.spec.ts
cd frontend && npm run build
ffmpeg -hide_banner -version && ffprobe -hide_banner -version
```

`go test ./mediaproc/ -run 'Fixture|Integration' -count=1` is the real FFmpeg run (same-format, alpha, animation, audio, orientation, GPS-like tags, truncated and oversized headers, and a privacy-only rewrite that must drop arbitrary tags). The Docker image build is attempted only when a daemon is available. This environment has no Docker daemon, so the image build stays unverified. The Dockerfile `RUN` invokes `ffmpeg`, `ffprobe`, and `prlimit`, so a missing binary fails the build. `prlimit` is not a network namespace: a decoder that opens a socket without going through FFmpeg's protocol layer is still not blocked by the kernel.
