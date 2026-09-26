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
