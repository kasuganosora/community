import type { MastodonAccount } from '@/shared/types/mastodon'
import { internalRouteForLink } from './links'
import { localHosts } from './env'

/** The reply chip already names this recipient. Only remove that exact leading
 * mention, never other recipients, ordinary text, or a mention later in a post. */
export function withoutLeadingReplyMention(html: string, recipient?: MastodonAccount): string {
  if (!recipient || typeof document === 'undefined') return html
  const template = document.createElement('template')
  template.innerHTML = html
  const container = template.content.firstElementChild?.tagName === 'P' ? template.content.firstElementChild : template.content
  let first = container.firstChild
  while (first?.nodeType === Node.TEXT_NODE && !first.textContent?.trim()) first = first.nextSibling
  if (!(first instanceof HTMLElement)) return html
  const anchor = first.matches('a.mention:not(.hashtag)') ? first : first.matches('span.h-card') ? first.querySelector('a.mention:not(.hashtag)') : null
  if (!anchor || first.textContent?.trim() !== anchor.textContent?.trim()) return html
  const href = anchor.getAttribute('href') || ''
  const path = internalRouteForLink(href, anchor.className, localHosts())
  if (!href || (href !== recipient.url && path !== `/@${recipient.acct}`)) return html
  first.remove()
  if (container.firstChild?.nodeType === Node.TEXT_NODE) container.firstChild.textContent = container.firstChild.textContent?.replace(/^\s+/, '') ?? ''
  return template.innerHTML
}
