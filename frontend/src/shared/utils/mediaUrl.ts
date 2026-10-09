// Uploads (and remote files relayed by the media proxy) are stored behind /media, but the API returns them as absolute
// URLs of the configured site (often http://127.0.0.1). The page may be
// opened through another host, so load those files from the current origin.
export function sameOriginMediaUrl(raw: string | null | undefined): string {
  if (!raw) return ''
  try {
    const url = new URL(raw, window.location.origin)
    if (url.pathname.startsWith('/media/') || url.pathname.startsWith('/proxy/media/')) return `${url.pathname}${url.search}`
  } catch {
    return raw
  }
  return raw
}
