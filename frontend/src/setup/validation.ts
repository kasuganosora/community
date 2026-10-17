/** Match the install API's origin rules while allowing a bare hostname. */
export function normalizeSetupDomain(value: string): string | null {
  try {
    const text = value.trim()
    if (!text || /[\s\\]/.test(text)) return null
    const url = new URL(text.includes('://') ? text : `https://${text}`)
    const local = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
    if (url.username || url.password || url.pathname !== '/' || url.search || url.hash ||
      (url.protocol !== 'https:' && !(local && url.protocol === 'http:')) || (!local && !url.hostname.includes('.'))) return null
    return url.origin
  } catch { return null }
}
