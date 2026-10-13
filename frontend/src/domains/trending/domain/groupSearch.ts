/** Detect room references only. Parsing/normalization, discovery and via validation
 * belong to the backend; a search must never join a room. */
export function isGroupReference(raw: string): boolean {
  const value = raw.trim()
  if (/^#[^\s]+:[^\s]+$/.test(value) || /^![^\s]+$/.test(value)) return true
  if (/^matrix:(?:r|roomid)\//i.test(value)) return true
  try {
    const url = new URL(value)
    if (url.protocol !== 'https:' || url.hostname !== 'matrix.to' || url.username || url.password) return false
    const target = decodeURIComponent(url.hash.replace(/^#\//, '').split('?')[0] || '')
    return /^[#!][^\s]+$/.test(target)
  } catch {
    return false
  }
}

/** Never put an untrusted protocol into a clickable remote client link. */
export function groupClientLink(raw: string | undefined): string {
  if (!raw) return ''
  try {
    const url = new URL(raw)
    return url.protocol === 'https:' && !url.username && !url.password ? url.href : ''
  } catch {
    return ''
  }
}

export function groupMembershipLabel(membership: string): string {
  return ({ join: '已加入', invite: '邀请待处理', knock: '等待批准', ban: '已被封禁' } as Record<string, string>)[membership] || ''
}
