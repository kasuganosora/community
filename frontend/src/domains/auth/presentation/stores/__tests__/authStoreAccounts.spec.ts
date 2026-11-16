import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

const api = vi.hoisted(() => ({
  login: vi.fn(),
  logout: vi.fn(async (_t?: string, _active?: string | null) => {}),
  verifyCredentials: vi.fn(),
}))
vi.mock('../../../infrastructure/authApi', () => ({ authApi: api }))
vi.mock('@/shared/composables/useMediaMode', () => ({ useMediaMode: () => ({ loadMode: vi.fn() }) }))

import { useAuthStore } from '../authStore'
import { getAccount, listAccounts, upsertAccount } from '@/shared/api/accounts'

const alice = { id: '1', username: 'alice', display_name: 'Alice', avatar: '' }
const bob = { id: '2', username: 'bob', display_name: 'Bob', avatar: '' }
const loc = { assign: vi.fn(), reload: vi.fn(), href: '' }

describe('auth store multi-account', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    vi.stubGlobal('location', loc)
    setActivePinia(createPinia())
    upsertAccount(alice, 'ta', 1)
    upsertAccount(bob, 'tb', 2)
    localStorage.setItem('auth_token', 'ta')
  })

  async function signedInAsAlice() {
    api.verifyCredentials.mockResolvedValueOnce(alice)
    const s = useAuthStore()
    await s.initialize()
    return s
  }

  it('switches by swapping the active token and reloading', async () => {
    const s = await signedInAsAlice()
    expect(s.switchAccount('2')).toBe('switched')
    expect(localStorage.getItem('auth_token')).toBe('tb')
    expect(loc.reload).toHaveBeenCalled()
    expect(s.switchAccount('1')).toBe('noop')
  })

  it('logout removes only the active account and falls back to the next', async () => {
    const s = await signedInAsAlice()
    expect(await s.logout()).toBe(true)
    expect(listAccounts().map(a => a.id)).toEqual(['2'])
    expect(localStorage.getItem('auth_token')).toBe('tb')
    expect(loc.assign).toHaveBeenCalledWith('/')
  })

  it('removing a non-active account revokes its own token', async () => {
    const s = await signedInAsAlice()
    expect(await s.removeAccount('2')).toBe(false)
    expect(api.logout).toHaveBeenCalledWith('tb')
    expect(localStorage.getItem('auth_token')).toBe('ta')
    expect(s.accounts.map(a => a.id)).toEqual(['1'])
  })

  it('a 401 marks the account as needing re-login and switches to another', async () => {
    const s = await signedInAsAlice()
    expect(s.expireSession()).toBe(true)
    expect(getAccount('1')).toMatchObject({ needsRelogin: true, token: '' })
    expect(localStorage.getItem('auth_token')).toBe('tb')
    expect(s.switchAccount('1')).toBe('relogin')
  })

  it('a 401 with no other usable account signs out', async () => {
    const s = await signedInAsAlice()
    await s.removeAccount('2')
    expect(s.expireSession()).toBe(false)
    expect(localStorage.getItem('auth_token')).toBeNull()
  })

  it.each([
    Object.assign(new Error('timeout'), { code: 'ECONNABORTED' }),
    Object.assign(new Error('offline'), { code: 'ERR_NETWORK' }),
    Object.assign(new Error('service unavailable'), { response: { status: 503 } }),
    Object.assign(new Error('request timeout'), { response: { status: 408 } }),
    Object.assign(new Error('too many requests'), { response: { status: 429 } }),
  ])('keeps the token when initialization fails transiently and can retry', async failure => {
    const s = useAuthStore()
    api.verifyCredentials.mockRejectedValueOnce(failure).mockResolvedValueOnce(alice)
    await expect(s.initialize()).rejects.toBe(failure)
    expect(localStorage.getItem('auth_token')).toBe('ta')
    expect(s.isAuthenticated).toBe(true)
    expect(getAccount('1')?.needsRelogin).toBe(false)
    expect(await s.initialize()).toBe(true)
    expect(s.currentUser?.id).toBe('1')
    expect(api.logout).not.toHaveBeenCalled()
  })

  it('keeps the loaded profile on a transient revalidation failure', async () => {
    const s = await signedInAsAlice()
    api.verifyCredentials.mockRejectedValueOnce(new Error('timeout'))
    expect(await s.fetchCurrentUser()).toBe(false)
    expect(s.currentUser?.id).toBe('1')
    expect(s.token).toBe('ta')
  })

  it('still clears an invalid session when initialization receives a 401', async () => {
    const s = useAuthStore()
    api.verifyCredentials.mockRejectedValueOnce({ response: { status: 401 } })
    expect(await s.initialize()).toBe(false)
    expect(s.currentUser).toBeNull()
    expect(s.token).toBeNull()
    expect(localStorage.getItem('auth_token')).toBeNull()
  })
  it.each([403, 422, 400])('treats a %i from verify_credentials as an invalid session and signs out', async status => {
    const s = useAuthStore()
    api.verifyCredentials.mockRejectedValueOnce(Object.assign(new Error('rejected'), { response: { status } }))
    expect(await s.initialize()).toBe(false)
    expect(s.currentUser).toBeNull()
    expect(s.token).toBeNull()
    expect(s.isAuthenticated).toBe(false)
    expect(localStorage.getItem('auth_token')).toBeNull()
    expect(getAccount('1')?.needsRelogin).toBe(true)
  })

  it('signs out a loaded session when revalidation gets a 403', async () => {
    const s = await signedInAsAlice()
    api.verifyCredentials.mockRejectedValueOnce({ response: { status: 403 } })
    expect(await s.fetchCurrentUser()).toBe(false)
    expect(s.currentUser).toBeNull()
    expect(s.token).toBeNull()
    expect(localStorage.getItem('auth_token')).toBeNull()
  })

  it('stores only encrypted persistent_token in localStorage on login instead of raw session token', async () => {
    localStorage.clear()
    setActivePinia(createPinia())
    const s = useAuthStore()
    api.login.mockResolvedValueOnce({
      message: 'login success',
      token: 'raw-cookie-session-token',
      persistent_token: 'pl1.encrypted-credential',
    })
    api.verifyCredentials.mockResolvedValueOnce(alice)

    await s.login({ username: 'alice', password: 'pw' })
    expect(localStorage.getItem('auth_token')).toBe('pl1.encrypted-credential')
    expect(getAccount('1')?.token).toBe('pl1.encrypted-credential')
  })

  it('adopts same-account rotated persistent credential from another tab without reloading, and reloads on account switch', async () => {
    const s = await signedInAsAlice()
    loc.reload.mockClear()

    // Simulate Tab 1 rotating Alice's persistent credential in localStorage
    upsertAccount(alice, 'pl1.alice-rotated', 1)
    localStorage.setItem('auth_token', 'pl1.alice-rotated')
    window.dispatchEvent(new StorageEvent('storage', { key: 'auth_token', oldValue: 'ta', newValue: 'pl1.alice-rotated' }))

    expect(loc.reload).not.toHaveBeenCalled()
    expect(s.token).toBe('pl1.alice-rotated')

    // Simulate Tab 1 switching active account to Bob -> Tab 2 must reload
    localStorage.setItem('auth_token', 'tb')
    window.dispatchEvent(new StorageEvent('storage', { key: 'auth_token', oldValue: 'pl1.alice-rotated', newValue: 'tb' }))
    expect(loc.reload).toHaveBeenCalledOnce()
  })
})
