import type { MastodonAccount } from '@/shared/types/mastodon'

/** Include the canonical account domain, not the host alias used to open the UI. */
export function avatarIdentity(account: Pick<MastodonAccount, 'acct' | 'username' | 'url' | 'uri'>): string {
  const acct = (account.acct || account.username || '').trim().replace(/^@/, '')
  const separator = acct.lastIndexOf('@')
  if (separator >= 0) return `${acct.slice(0, separator)}@${acct.slice(separator + 1).toLowerCase()}`
  for (const raw of [account.url, account.uri]) {
    try {
      const url = new URL(raw)
      if ((url.protocol === 'https:' || url.protocol === 'http:') && !url.username && !url.password) {
        return `${acct}@${url.host.toLowerCase()}`
      }
    } catch { /* Partial account fixtures can omit canonical URLs. */ }
  }
  return `${acct}@${window.location.host.toLowerCase()}`
}

/** Match the server's generated PNG, without canvas, disk or remote SVG markup. */
export function identiconPattern(identity: string, defaultUrl?: string): { background: string; foreground: string; cells: { x: number; y: number }[] } {
  const seed = defaultUrl?.match(/\/([a-f0-9]{64})\.png(?:[?#]|$)/)?.[1]
  let bytes: number[]
  if (seed) {
    bytes = Array.from({ length: 32 }, (_, i) => Number.parseInt(seed.slice(i * 2, i * 2 + 2), 16))
  } else {
    // Legacy/partial account responses may lack avatar_default. Keep those
    // distinguishable too, without introducing asynchronous hashing or requests.
    let hash = 2166136261
    for (const byte of new TextEncoder().encode(identity.trim())) hash = Math.imul(hash ^ byte, 16777619) >>> 0
    bytes = Array.from({ length: 5 }, () => {
      hash = Math.imul(hash ^ (hash >>> 13), 16777619) >>> 0
      return hash & 255
    })
  }
  const cells: { x: number; y: number }[] = []
  for (let y = 0; y < 5; y++) {
    for (let x = 0; x < 3; x++) {
      const bit = y * 3 + x
      if ((x === 2 && y === 2) || (((bytes[3 + Math.floor(bit / 8)] ?? 0) >>> (bit % 8)) & 1)) {
        cells.push({ x: 14 + x * 20, y: 14 + y * 20 })
        if (x !== 2) cells.push({ x: 14 + (4 - x) * 20, y: 14 + y * 20 })
      }
    }
  }
  return {
    background: 'rgb(240,243,248)',
    foreground: `rgb(${48 + bytes[0]! % 128},${48 + bytes[1]! % 128},${48 + bytes[2]! % 128})`,
    cells,
  }
}
