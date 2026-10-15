/**
 * Whether a search should ask the server to resolve remotely (WebFinger or
 * fetching a URL). Only an account handle or a link can be resolved; plain
 * keywords stay local, so they never wait on remote servers.
 */
export function wantsResolve(q: string): boolean {
  const s = q.trim()
  return /^@?[^\s@/]+@[^\s@/]+\.[^\s@/]+$/.test(s) || /^https?:\/\/\S+$/i.test(s)
}
