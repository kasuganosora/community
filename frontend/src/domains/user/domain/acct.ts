import type { MastodonAccount } from '@/shared/types/mastodon'

/** Strip a leading '@' and lower-case an acct / username for comparison. */
export function normalizeAcct(acct: string): string {
  return acct.trim().replace(/^@/, '').toLowerCase()
}

/**
 * Pick the account that a profile URL (/@user or /@user@domain) refers to
 * from account-search results.
 *
 * /accounts/search is a prefix match that returns e.g. both "al" and "alice"
 * for "al". So never just take the first hit: prefer an exact acct match,
 * then (for a bare username, or our own domain) an exact local username
 * match. A remote acct never matches an account on another domain.
 */
export function pickAccountByAcct(
  accounts: MastodonAccount[],
  acct: string,
  localDomain?: string,
): MastodonAccount | undefined {
  const wanted = normalizeAcct(acct)
  if (!wanted) return undefined
  const exact = accounts.find(a => normalizeAcct(a.acct) === wanted)
  if (exact) return exact
  const [user, domain] = wanted.split('@')
  return accounts.find((a) => {
    if ((a.username || '').toLowerCase() !== user) return false
    if (!domain) return true
    // acct "alice" (local, Mastodon style) requested as alice@our-domain
    return !a.acct.includes('@') && !!localDomain && domain === localDomain.toLowerCase()
  })
}
