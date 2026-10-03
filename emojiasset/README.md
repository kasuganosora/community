# Emoji assets

`StoreEmojiImage(ctx, reader, declaredType)` validates and saves an original image plus a static PNG preview. `ImportEmojiImage(ctx, url)` downloads through the existing federation SSRF/TLS transport before applying the same validation. Both return `*Asset`; `DeleteAsset` cleans up newly created local files if a database operation fails.

Limits: 2 MiB encoded bytes, 1024×1024 canvas, 128 animation frames, and 16 million total frame pixels. PNG, APNG, JPEG, GIF, and static/animated WebP are supported. APNG/WebP frames are bounded and validated with the existing PNG/VP8/VP8L decoders; the original animation is preserved and the actual first animation frame becomes the preview. No multimedia subprocess or new dependency is required.

Remote reads have a 15-second deadline, a hard streaming size limit, strict certificate checks, and redirect validation. Credentials in URLs, non-HTTP schemes, HTTPS downgrades, private-address redirects, and oversized responses are rejected. Existing experimental federation peer exceptions remain exact hostname/IP rules.

Files live under `uploads/emojis/` with generated UUID filenames and are served by `Serve` at `/emoji-assets/:file` with image MIME types and `nosniff`. SVG, HTML, incorrect declared MIME, unsupported formats, malformed images, and traversal filenames are rejected.

`testdata/` contains tiny generated red/green APNG and animated WebP fixtures, including an APNG whose default image is not part of its animation.
