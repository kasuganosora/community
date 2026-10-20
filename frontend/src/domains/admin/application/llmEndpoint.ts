/**
 * Mirrors llmsvc.ResolveChatPath / ChatEndpoint on the server so the admin card
 * can preview where an OpenAI-compatible provider's requests go.
 */
export const DEFAULT_CHAT_PATH = '/v1/chat/completions'

const isAbsolute = (s: string) => /^https?:\/\//i.test(s)

function lastSegmentIsVersion(base: string): boolean {
  let path = base
  try { path = new URL(base).pathname } catch { /* keep raw */ }
  const segs = path.replace(/\/+$/, '').split('/')
  return /^v\d+([a-z]+\d*)?$/.test((segs[segs.length - 1] || '').toLowerCase())
}

export function resolveChatPath(base: string, chatPath: string): string {
  const p = chatPath.trim()
  if (p) return isAbsolute(p) ? p : `/${p.replace(/^\/+/, '')}`
  const b = base.trim().replace(/\/+$/, '')
  if (/\/chat\/completions$/i.test(b)) return b
  return lastSegmentIsVersion(b) ? '/chat/completions' : DEFAULT_CHAT_PATH
}

export function chatEndpoint(base: string, chatPath: string): string {
  const path = resolveChatPath(base, chatPath)
  if (isAbsolute(path)) return path
  return `${base.trim().replace(/\/+$/, '')}/${path.replace(/^\/+/, '')}`
}
