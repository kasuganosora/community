/**
 * Multi-account registry (Misskey-style account switching).
 *
 * Every signed-in account on this device is kept in localStorage under
 * ACCOUNTS_KEY, keyed by account id, with its own bearer token and a small
 * profile snapshot for the switcher. The *active* token stays in
 * AUTH_TOKEN_KEY (read by the API client, router guard and feed stream), so
 * switching means: swap that one key, then reload so every per-user store,
 * cache and stream starts clean for the new identity.
 *
 * Tokens never leave localStorage except as an Authorization header.
 */
export const ACCOUNTS_KEY = 'auth_accounts_v1'

export interface StoredAccount {
  id: string
  username: string
  acct?: string
  display_name: string
  avatar: string
  /** Bearer token; empty when the account needs to sign in again. */
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

/** Insert or refresh an account with a working token. */
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
  // One token belongs to one account: drop stale duplicates.
  for (const [k, v] of Object.entries(reg)) if (k !== profile.id && v.token === token) delete reg[k]
  reg[profile.id] = next
  write(reg)
  return next
}

export function removeAccount(id: string): StoredAccount | null {
  const reg = readAccounts()
  const gone = reg[id] ?? null
  if (gone) { delete reg[id]; write(reg) }
  return gone
}

/** Token rejected (401): forget the token but keep the entry so the user can sign in again. */
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

/** Next account to fall back to after `excludeId` leaves (must have a usable token). */
export function nextUsableAccount(excludeId?: string | null): StoredAccount | null {
  return listAccounts().find(a => a.id !== excludeId && !a.needsRelogin && a.token !== '') ?? null
}

export function clearAccounts() {
  try { storage()?.removeItem(ACCOUNTS_KEY) } catch { /* ignore */ }
}
