import type { MastodonAccount } from '@/shared/types/mastodon'

/** Bridge text may expand a mention to "@name@host (profile URL)".
 * Collapse only that matching profile link; ordinary parentheses and links stay. */
export function messagePreview(text: string, accounts: MastodonAccount[] = []): string {
  const compact = text.replace(/(@[^\s()[\]<>]+)\s+\((https?:\/\/[^\s()]+)\)/gu, (original, mention: string, href: string) => {
    try {
      const url = new URL(href)
      if (url.username || url.password || url.search || url.hash) return original
      const handle = mention.slice(1)
      const split = handle.lastIndexOf('@')
      const user = split < 0 ? handle : handle.slice(0, split)
      const host = split < 0 ? '' : handle.slice(split + 1)
      const matchingProfile = host.toLowerCase() === url.host.toLowerCase() && [`/@${user}`, `/users/${user}`].includes(decodeURIComponent(url.pathname).replace(/\/$/, ''))
      const knownProfile = accounts.some(a => a.url === href && (a.acct === handle || a.username === handle))
      return matchingProfile || knownProfile ? mention : original
    } catch { return original }
  }).replace(/\s+/g, ' ').trim()

  const mentions = new Set(accounts.flatMap(account => {
    const handles = [`@${account.acct}`]
    try { if (!account.acct.includes('@')) handles.push(`@${account.username}@${new URL(account.url).host}`) } catch { /* An account may have no profile URL. */ }
    return handles
  }))
  let remaining = compact
  while (remaining) {
    const leading = remaining.match(/^(@\S+)\s+/)?.[1]
    if (!leading || !mentions.has(leading)) break
    remaining = remaining.slice(leading.length).trimStart()
  }
  return remaining || compact
}
