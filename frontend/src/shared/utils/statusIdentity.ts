/** Identity of a timeline row. Optional fields are absent on non-status lists. */
export interface StatusIdentity {
  id: string
  uri?: string | null
  url?: string | null
  account?: { acct?: string | null } | null
  reblog?: { id?: string | null } | null
}

/** Fields a timeline socket may carry besides the status id. */
export interface StreamedStatusRef {
  id: string
  uri?: string | null
  url?: string | null
  acct?: string | null
  reblogId?: string | null
}

function clean(value?: string | null): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** Local permalinks: /users/:name/statuses/:id, /@:name/status/:id, /@:name/:id. */
export function embeddedLocalID(value?: string | null): string | null {
  const text = clean(value)
  if (!text) return null
  let path = text
  if (text.includes('://')) {
    let parsed: URL
    try { parsed = new URL(text) } catch { return null }
    if (typeof window !== 'undefined' && parsed.hostname.toLowerCase() !== window.location.hostname.toLowerCase()) return null
    path = parsed.pathname
  }
  const parts = path.split('/').filter(Boolean)
  const id = (raw?: string) => raw && /^\d+$/.test(raw) ? raw : null
  if (parts.length === 4 && parts[0] === 'users' && parts[2] === 'statuses') return id(parts[3])
  if (parts.length === 3 && parts[0]?.startsWith('@') && parts[1] === 'status') return id(parts[2])
  if (parts.length === 2 && parts[0]?.startsWith('@')) return id(parts[1])
  return null
}

function identityKeys(row: StatusIdentity): string[] {
  const keys = [`id:${row.id}`]
  for (const value of [row.uri, row.url]) {
    const text = clean(value)
    if (!text) continue
    keys.push(`uri:${text}`)
    const embedded = embeddedLocalID(text)
    if (embedded && embedded !== row.id) keys.push(`id:${embedded}`)
  }
  return keys
}

export function isLocalAcct(row: StatusIdentity): boolean {
  const acct = clean(row.account?.acct)
  return acct !== '' && !acct.includes('@')
}

/** Remote acct of the same local person, addressed on this instance. */
export function isRemoteSelf(acct: string | null | undefined, localAcct: string | null | undefined): boolean {
  const remote = clean(acct)
  const local = clean(localAcct)
  const at = remote.lastIndexOf('@')
  if (!local || local.includes('@') || at < 1) return false
  if (remote.slice(0, at).toLowerCase() !== local.toLowerCase()) return false
  if (typeof window === 'undefined') return false
  const domain = remote.slice(at + 1).toLowerCase()
  return domain === window.location.hostname.toLowerCase() || domain === window.location.host.toLowerCase()
}

function isRemoteSelfMirror(row: StatusIdentity, original: StatusIdentity): boolean {
  return !!row.reblog?.id && row.reblog.id === original.id && isLocalAcct(original) && isRemoteSelf(row.account?.acct, original.account?.acct)
}

function chooseWinner(rows: readonly StatusIdentity[], left: number, right: number): number {
  const prefer = (index: number) => isLocalAcct(rows[index]!) && !isLocalAcct(rows[index === left ? right : left]!)
  if (prefer(left)) return left
  if (prefer(right)) return right
  return Math.min(left, right)
}

/**
 * One card per object. A remote mirror that shares the local status URI,
 * permalink id, or a same-person reblog is dropped. The local acct wins.
 * Rows that are only the same id stay as the first copy.
 */
export function dedupeMirroredStatuses<T extends { id: string }>(rows: readonly T[]): T[] {
  const count = rows.length
  if (count < 2) return [...rows]
  const identities = rows as readonly StatusIdentity[]
  const parent = Array.from({ length: count }, (_, index) => index)
  const find = (index: number): number => parent[index] === index ? index : (parent[index] = find(parent[index]!))
  const union = (left: number, right: number) => {
    const a = find(left)
    const b = find(right)
    if (a !== b) parent[b] = a
  }
  const owner = new Map<string, number>()
  identities.forEach((row, index) => {
    for (const key of identityKeys(row)) {
      const previous = owner.get(key)
      if (previous === undefined) owner.set(key, index)
      else union(previous, index)
    }
    if (row.reblog?.id) {
      const original = identities.findIndex(item => item.id === row.reblog?.id)
      if (original >= 0 && isRemoteSelfMirror(row, identities[original]!)) union(index, original)
    }
  })
  const members = new Map<number, number[]>()
  for (let index = 0; index < count; index++) {
    const root = find(index)
    const group = members.get(root)
    if (group) group.push(index)
    else members.set(root, [index])
  }
  const drop = new Set<number>()
  for (const indexes of members.values()) {
    if (indexes.length < 2) continue
    let best = indexes[0]!
    for (const index of indexes) best = chooseWinner(identities, best, index)
    for (const index of indexes) if (index !== best) drop.add(index)
  }
  return drop.size ? rows.filter((_, index) => !drop.has(index)) : [...rows]
}

/** True when a streamed id is already represented by a visible row. */
export function sharesStatusIdentity(row: { id: string }, incoming: StreamedStatusRef): boolean {
  const identity = row as StatusIdentity
  if (identity.id === incoming.id || (identity.reblog?.id && identity.reblog.id === incoming.id)) return true
  const keys = new Set(identityKeys(identity))
  if (identityKeys(incoming).some(key => keys.has(key))) return true
  return !!incoming.reblogId && incoming.reblogId === identity.id && isLocalAcct(identity) && isRemoteSelf(incoming.acct, identity.account?.acct)
}
