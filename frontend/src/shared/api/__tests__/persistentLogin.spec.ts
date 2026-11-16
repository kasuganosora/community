import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AxiosError, type AxiosAdapter } from 'axios'
import client, { setAuthExpiredCallback, tokenService } from '@/shared/api/client'
import {
  clearAccounts,
  getAccount,
  hasPendingCookieExchange,
  setActiveAccountId,
  setPendingCookieExchange,
  upsertAccount,
} from '@/shared/api/accounts'
import { clearAppealSession, endLocalSignOut } from '@/shared/api/appealSession'

const alice = { id: '1', username: 'alice', display_name: 'Alice', avatar: 'a.png' }
const bob = { id: '2', username: 'bob', display_name: 'Bob', avatar: 'b.png' }

beforeEach(() => {
  localStorage.clear()
  clearAccounts()
  clearAppealSession()
  endLocalSignOut()
  vi.restoreAllMocks()
})

afterEach(() => {
  setAuthExpiredCallback(() => {})
  clearAppealSession()
  endLocalSignOut()
  localStorage.clear()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('persistent login cookie exchange & token rotation', () => {
  it('authenticates daily requests via cookies without exposing persistent credentials in Authorization header', async () => {
    upsertAccount(alice, 'pl1.alice-cred-1', 1)
    tokenService.set('pl1.alice-cred-1')

    let seenAuthHeader: unknown
    let seenWithCredentials: boolean | undefined
    const adapter: AxiosAdapter = async (config) => {
      seenAuthHeader = config.headers?.Authorization
      seenWithCredentials = config.withCredentials
      return { data: { ok: true }, status: 200, statusText: 'OK', headers: {}, config }
    }

    const res = await client.get('/api/v1/timelines/home', { adapter })
    expect(res.data).toEqual({ ok: true })
    expect(seenWithCredentials).toBe(true)
    expect(seenAuthHeader).toBeUndefined()
  })

  it('exchanges persistent credential on 401, rotates localStorage & multi-account registry, and retries once', async () => {
    upsertAccount(alice, 'pl1.alice-cred-1', 1)
    upsertAccount(bob, 'pl1.bob-cred-1', 2)
    setActiveAccountId('1')
    tokenService.set('pl1.alice-cred-1')

    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      expect(url).toBe('/api/v1/accounts/session/refresh')
      expect(init?.credentials).toBe('include')
      expect(JSON.parse(String(init?.body))).toEqual({ persistent_token: 'pl1.alice-cred-1' })
      return new Response(
        JSON.stringify({ message: 'login success', token: 'new-cookie-session', persistent_token: 'pl1.alice-cred-2' }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    })
    vi.stubGlobal('fetch', fetchMock)

    let attempts = 0
    const adapter: AxiosAdapter = async (config) => {
      attempts++
      if (attempts === 1) {
        throw new AxiosError('unauthorized', 'ERR_BAD_REQUEST', config, undefined, {
          status: 401,
          statusText: 'Unauthorized',
          headers: {},
          config,
          data: { error: 'unauthorized' },
        })
      }
      return { data: { id: '1', username: 'alice' }, status: 200, statusText: 'OK', headers: {}, config }
    }

    const res = await client.get('/api/v1/accounts/verify_credentials', { adapter })
    expect(res.data).toEqual({ id: '1', username: 'alice' })
    expect(attempts).toBe(2)
    expect(fetchMock).toHaveBeenCalledOnce()

    // Active credential and Alice's multi-account slot rotated; Bob untouched; raw cookie token never stored.
    expect(tokenService.get()).toBe('pl1.alice-cred-2')
    expect(getAccount('1')?.token).toBe('pl1.alice-cred-2')
    expect(getAccount('2')?.token).toBe('pl1.bob-cred-1')
  })

  it('coalesces concurrent 401 responses into a single one-time persistent token exchange', async () => {
    upsertAccount(alice, 'pl1.single-flight-1', 1)
    setActiveAccountId('1')
    tokenService.set('pl1.single-flight-1')

    let cookieRefreshed = false
    const fetchMock = vi.fn(async () => {
      await new Promise(resolve => setTimeout(resolve, 10))
      cookieRefreshed = true
      return new Response(
        JSON.stringify({ message: 'login success', token: 'cookie-2', persistent_token: 'pl1.single-flight-2' }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    })
    vi.stubGlobal('fetch', fetchMock)

    const adapter: AxiosAdapter = async (config) => {
      if (!cookieRefreshed) {
        throw new AxiosError('unauthorized', 'ERR_BAD_REQUEST', config, undefined, {
          status: 401,
          statusText: 'Unauthorized',
          headers: {},
          config,
          data: { error: 'unauthorized' },
        })
      }
      return { data: { url: config.url }, status: 200, statusText: 'OK', headers: {}, config }
    }

    const [r1, r2, r3] = await Promise.all([
      client.get('/api/v1/timelines/home', { adapter }),
      client.get('/api/v1/notifications', { adapter }),
      client.get('/api/v1/unread_counts', { adapter }),
    ])

    expect(fetchMock).toHaveBeenCalledOnce()
    expect(r1.status).toBe(200)
    expect(r2.status).toBe(200)
    expect(r3.status).toBe(200)
    expect(tokenService.get()).toBe('pl1.single-flight-2')
  })

  it('exchanges target account credential before sending requests after multi-account switch', async () => {
    upsertAccount(alice, 'pl1.alice-1', 1)
    upsertAccount(bob, 'pl1.bob-1', 2)
    setActiveAccountId('2')
    setPendingCookieExchange(true)
    tokenService.set('pl1.bob-1')

    const callOrder: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init?: RequestInit) => {
      callOrder.push(`refresh:${JSON.parse(String(init?.body)).persistent_token}`)
      return new Response(
        JSON.stringify({ message: 'login success', token: 'bob-cookie', persistent_token: 'pl1.bob-2' }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    }))

    const adapter: AxiosAdapter = async (config) => {
      callOrder.push(`request:${config.url}`)
      return { data: bob, status: 200, statusText: 'OK', headers: {}, config }
    }

    const res = await client.get('/api/v1/accounts/verify_credentials', { adapter })
    expect(res.data).toEqual(bob)
    expect(callOrder).toEqual(['refresh:pl1.bob-1', 'request:/api/v1/accounts/verify_credentials'])
    expect(hasPendingCookieExchange()).toBe(false)
    expect(tokenService.get()).toBe('pl1.bob-2')
    expect(getAccount('2')?.token).toBe('pl1.bob-2')
    expect(getAccount('1')?.token).toBe('pl1.alice-1')
  })

  it('invokes authExpiredCallback and clears dead persistent credential when refresh returns 401', async () => {
    const expired = vi.fn()
    setAuthExpiredCallback(expired)
    upsertAccount(alice, 'pl1.revoked', 1)
    tokenService.set('pl1.revoked')

    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"error":"invalid persistent token"}', { status: 401 })))

    const adapter: AxiosAdapter = async (config) => {
      throw new AxiosError('unauthorized', 'ERR_BAD_REQUEST', config, undefined, {
        status: 401,
        statusText: 'Unauthorized',
        headers: {},
        config,
        data: { error: 'unauthorized' },
      })
    }

    await expect(client.get('/api/v1/timelines/home', { adapter })).rejects.toThrow()
    expect(expired).toHaveBeenCalledOnce()
    expect(tokenService.get()).toBeNull()
  })
})
