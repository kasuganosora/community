import { toUnicode } from 'punycode/'

/** Presentation only. Never use the result as an account ID or request URL. */
export function displayDomain(domain: string): string {
  if (!domain.toLowerCase().includes('xn--')) return domain
  const parts = /^([a-z0-9.-]+)(:\d+)?$/i.exec(domain)
  if (!parts?.[1]) return domain
  try {
    const decoded = toUnicode(parts[1].toLowerCase())
    // Validate IDNA round-tripping; malformed labels remain visible as received.
    if (new URL(`https://${decoded}`).hostname !== parts[1].toLowerCase()) return domain
    return decoded + (parts[2] || '')
  } catch { return domain }
}

export function displayAccount(account?: string | null): string {
  if (!account) return ''
  const matrix = /^(@[^@:]+:)(.+)$/.exec(account)
  if (matrix?.[1] && matrix[2]) return matrix[1] + displayDomain(matrix[2])
  const separator = account.lastIndexOf('@')
  if (separator < 1) return account
  return account.slice(0, separator + 1) + displayDomain(account.slice(separator + 1))
}
