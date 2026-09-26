/**
 * Mock data is opt-in: set VITE_USE_MOCK=true when running the dev server
 * without a backend. It used to be enabled implicitly whenever VITE_API_URL
 * was unset in dev, so `npm run dev` against the proxied backend silently
 * logged in with a fake token and showed mock timelines.
 */
export function isMockEnabled(env: { DEV?: boolean; VITE_USE_MOCK?: string } = import.meta.env): boolean {
  return !!env.DEV && env.VITE_USE_MOCK === 'true'
}

export const USE_MOCK = isMockEnabled()

/** Hosts that serve this instance (page host, API host, dev proxy target). */
export function localHosts(
  env: { VITE_API_URL?: string; VITE_DEV_PROXY_TARGET?: string } = import.meta.env,
  pageHost: string = typeof window !== 'undefined' ? window.location.host : '',
): string[] {
  const hosts = new Set<string>()
  if (pageHost) hosts.add(pageHost)
  for (const u of [env.VITE_API_URL, env.VITE_DEV_PROXY_TARGET]) {
    if (!u) continue
    try {
      hosts.add(new URL(u).host)
    } catch {
      // ignore malformed values
    }
  }
  return [...hosts]
}
