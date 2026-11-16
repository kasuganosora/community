/**
 * Multi-account registry (Misskey-style account switching).
 *
 * Every signed-in account on this device is kept in localStorage under
 * ACCOUNTS_KEY, keyed by account id, with its own server-encrypted persistent
 * login credential and a small profile snapshot for the switcher. Daily API
 * and streaming requests authenticate via the HttpOnly `app-token` cookie;
 * the encrypted persistent credential in localStorage is sent only to the
 * session-refresh / persistent-revoke endpoints when exchanging an expired
 * cookie or switching/signing out accounts, and is rotated on every exchange.
 */
export const ACCOUNTS_KEY = 'auth_accounts_v1'
export const ACTIVE_ACCOUNT_ID_KEY = 'auth_active_account_id'
export const PENDING_COOKIE_EXCHANGE_KEY = 'auth_cookie_exchange_needed'

export interface StoredAccount {
  id: string
  username: string
  acct?: string
  display_name: string
  avatar: string
  /** Server-encrypted persistent login credential; empty when re-login is needed. */
  token: string
  needsRelogin?: boolean
  addedAt: number
}

export interface AccountProfile {
  id: string
  username: string
  acct?: string
  display_name?: string
  avatar?: string
}

type Registry = Record<string, StoredAccount>
type TokenChangeListener = (nextToken: string | null) => void

const tokenChangeListeners = new Set<TokenChangeListener>()

export function onPersistentTokenChange(listener: TokenChangeListener): () => void {
  tokenChangeListeners.add(listener)
  return () => tokenChangeListeners.delete(listener)
}

export function notifyPersistentTokenChange(nextToken: string | null): void {
  for (const listener of tokenChangeListeners) {
    try { listener(nextToken) } catch (e) { console.error(e) }
  }
}

function storage(): Storage | null {
  try { return globalThis.localStorage ?? null } catch { return null }
}

function isAccount(v: unknown): v is StoredAccount {
  if (!v || typeof v !== 'object') return false
  const a = v as Record<string, unknown>
  return typeof a.id === 'string' && a.id !== '' && typeof a.username === 'string' && typeof a.token === 'string'
}

export function readAccounts(): Registry {
  const raw = storage()?.getItem(ACCOUNTS_KEY)
  if (!raw) return {}
  try {
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object') return {}
    const out: Registry = {}
    for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
      if (isAccount(v) && v.id === k) out[k] = v
    }
    return out
  } catch {
    return {}
  }
}

function write(reg: Registry) {
  try { storage()?.setItem(ACCOUNTS_KEY, JSON.stringify(reg)) } catch { /* storage full/disabled */ }
}

/** Accounts in a stable order (oldest added first). */
export function listAccounts(): StoredAccount[] {
  return Object.values(readAccounts()).sort((a, b) => a.addedAt - b.addedAt || a.id.localeCompare(b.id))
}

export function getAccount(id: string): StoredAccount | null {
  return readAccounts()[id] ?? null
}

/** Insert or refresh an account with a working persistent credential. */
export function upsertAccount(profile: AccountProfile, token: string, now = Date.now()): StoredAccount {
  const reg = readAccounts()
  const prev = reg[profile.id]
  const next: StoredAccount = {
    id: profile.id,
    username: profile.username,
    acct: profile.acct,
    display_name: profile.display_name ?? '',
    avatar: profile.avatar ?? '',
    token,
    needsRelogin: false,
    addedAt: prev?.addedAt ?? now,
  }
  // One credential belongs to one account: drop stale duplicates.
  for (const [k, v] of Object.entries(reg)) if (k !== profile.id && v.token === token) delete reg[k]
  reg[profile.id] = next
  write(reg)
  return next
}

/**
 * Replace a consumed persistent credential with its newly rotated replacement
 * while keeping the account's profile and addedAt metadata intact.
 */
export function rotateAccountToken(oldToken: string, newToken: string, fallbackAccountId?: string | null): StoredAccount | null {
  if (!newToken) return null
  const reg = readAccounts()
  let target = oldToken ? Object.values(reg).find(a => a.token === oldToken) : undefined
  if (!target && fallbackAccountId) target = reg[fallbackAccountId]
  if (!target) return null
  for (const [k, v] of Object.entries(reg)) {
    if (k !== target.id && v.token === newToken) delete reg[k]
  }
  target.token = newToken
  target.needsRelogin = false
  write(reg)
  return target
}

export function removeAccount(id: string): StoredAccount | null {
  const reg = readAccounts()
  const gone = reg[id] ?? null
  if (gone) { delete reg[id]; write(reg) }
  return gone
}

/** Credential rejected (401): forget the credential but keep the entry so the user can sign in again. */
export function markNeedsRelogin(token: string): StoredAccount | null {
  const reg = readAccounts()
  const hit = Object.values(reg).find(a => a.token === token && token !== '')
  if (!hit) return null
  hit.token = ''
  hit.needsRelogin = true
  write(reg)
  return hit
}

export function findByToken(token: string | null): StoredAccount | null {
  if (!token) return null
  return Object.values(readAccounts()).find(a => a.token === token) ?? null
}

/** Next account to fall back to after `excludeId` leaves (must have a usable credential). */
export function nextUsableAccount(excludeId?: string | null): StoredAccount | null {
  return listAccounts().find(a => a.id !== excludeId && !a.needsRelogin && a.token !== '') ?? null
}

export function getActiveAccountId(): string | null {
  try { return storage()?.getItem(ACTIVE_ACCOUNT_ID_KEY) || null } catch { return null }
}

export function setActiveAccountId(id: string | null): void {
  try {
    if (id) storage()?.setItem(ACTIVE_ACCOUNT_ID_KEY, id)
    else storage()?.removeItem(ACTIVE_ACCOUNT_ID_KEY)
  } catch { /* ignore */ }
}

export function hasPendingCookieExchange(): boolean {
  try { return storage()?.getItem(PENDING_COOKIE_EXCHANGE_KEY) === '1' } catch { return false }
}

export function setPendingCookieExchange(needed: boolean): void {
  try {
    if (needed) storage()?.setItem(PENDING_COOKIE_EXCHANGE_KEY, '1')
    else storage()?.removeItem(PENDING_COOKIE_EXCHANGE_KEY)
  } catch { /* ignore */ }
}

export function clearAccounts() {
  try {
    storage()?.removeItem(ACCOUNTS_KEY)
    storage()?.removeItem(ACTIVE_ACCOUNT_ID_KEY)
    storage()?.removeItem(PENDING_COOKIE_EXCHANGE_KEY)
  } catch { /* ignore */ }
}
