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

/** Storage URLs may use HTTP and a public media path; endpoints use an origin. */
export function normalizeStorageURL(value: string, endpoint = false): string | null {
  try {
    const text = value.trim()
    if (!text || /[\s\\]/.test(text)) return null
    const url = new URL(endpoint && !text.includes('://') ? `https://${text}` : text)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || (endpoint && url.pathname !== '/')) return null
    return url.href.replace(/\/+$/, '')
  } catch { return null }
}
