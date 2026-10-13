const SECTIONS = ['explore', 'announcements', 'notifications', 'messages', 'bookmarks', 'lists', 'settings'] as const
export type NavSection = 'home' | 'profile' | 'groups' | (typeof SECTIONS)[number] | ''

/**
 * Which primary nav entry a path belongs to. Matches whole path segments
 * (so /@alice/status/1 is not "profile" and /@alicex is not /@alice);
 * a status detail page belongs to no section.
 */
export function activeNavSection(path: string, username?: string | null): NavSection {
  if (path === '/' || path === '') return 'home'
  if (path === '/messages/groups' || path.startsWith('/messages/groups/')) return 'groups'
  if (path.startsWith('/search') || path.startsWith('/tags/')) return 'explore'
  for (const s of SECTIONS) {
    if (path === `/${s}` || path.startsWith(`/${s}/`)) return s
  }
  if (username) {
    const base = `/@${username}`.toLowerCase()
    const p = path.toLowerCase()
    if (p === base || p === `${base}/followers` || p === `${base}/following`) return 'profile'
  }
  return ''
}
