import axios, { AxiosError } from 'axios'
import type { InternalAxiosRequestConfig } from 'axios'
import type { ApiErrorData } from '@/shared/types/mastodon'
import { clearAppealSession, isAppealSession, isLocalSignOut, markAppealSession } from './appealSession'
import {
  getActiveAccountId,
  hasPendingCookieExchange,
  notifyPersistentTokenChange,
  onPersistentTokenChange,
  rotateAccountToken,
  setPendingCookieExchange,
} from './accounts'
import { applyStallRetry } from './stallRetry'
import { isSlowConnection, networkInformation, type NetworkInformationLike } from '@/shared/utils/network'

import { t } from '@/shared/i18n'
import { translateApiError } from '@/shared/i18n/apiErrors'

export { onPersistentTokenChange }

// Constants
export const AUTH_TOKEN_KEY = 'auth_token'
export const API_BASE_URL = import.meta.env.VITE_API_URL || ''

// Request timeout. Slow links (2G/3G or Data Saver, per the Network
// Information API where available) get more time: a 20-item remote timeline
// can take well over 15 s on such a connection.
export const DEFAULT_TIMEOUT_MS = 15000
export const SLOW_NETWORK_TIMEOUT_MS = 45000
export function requestTimeout(conn: NetworkInformationLike | undefined = networkInformation()): number {
  return isSlowConnection(conn) ? SLOW_NETWORK_TIMEOUT_MS : DEFAULT_TIMEOUT_MS
}

interface SessionTrackedConfig extends InternalAxiosRequestConfig {
  _sessionToken?: string | null
  _persistentRetried?: boolean
}

const client = axios.create({
  baseURL: API_BASE_URL,
  timeout: DEFAULT_TIMEOUT_MS,
  withCredentials: true,
  headers: {
    'Content-Type': 'application/json',
  },
})

// --- Persistent credential storage (HttpOnly cookie handles daily request auth) ---
export const tokenService = {
  get(): string | null {
    try {
      return localStorage.getItem(AUTH_TOKEN_KEY)
    } catch {
      return null
    }
  },
  set(token: string, options?: { preserveAppeal?: boolean }): void {
    if (!options?.preserveAppeal && token !== this.get()) clearAppealSession()
    try {
      localStorage.setItem(AUTH_TOKEN_KEY, token)
    } catch {
      // storage might be full or disabled
    }
  },
  clear(): void {
    clearAppealSession()
    try {
      localStorage.removeItem(AUTH_TOKEN_KEY)
    } catch {
      // ignore
    }
  },
}

// --- Auth expiry callback — set by router/app, avoids hard window.location redirect ---
let authExpiredCallback: (() => void) | null = null

export function setAuthExpiredCallback(cb: () => void) {
  authExpiredCallback = cb
}

// Reentrancy guard: when multiple requests fail with 401 simultaneously,
// only the first one triggers the auth-expired flow.
let isHandling401 = false
let appealProbe: { token: string; promise: Promise<boolean> } | null = null
let inflightSessionRefresh: { token: string; promise: Promise<PersistentRefreshOutcome> } | null = null

export type PersistentRefreshOutcome = 'refreshed' | 'invalid' | 'transient'

export const TIMEOUT_MESSAGE = t('shared.client.s1')
export const OFFLINE_MESSAGE = t('shared.client.s2')

function triggerAuthExpired(expectedToken: string | null) {
  if (!expectedToken || expectedToken !== tokenService.get() || isHandling401) return
  isHandling401 = true
  try {
    const beforeCallback = tokenService.get()
    if (authExpiredCallback) {
      try {
        authExpiredCallback()
      } catch (cbErr) {
        console.error('Auth expired callback failed:', cbErr)
      }
    }
    if (beforeCallback && beforeCallback === tokenService.get()) tokenService.clear()
    if (!authExpiredCallback) {
      window.location.href = '/login'
    }
  } finally {
    setTimeout(() => { isHandling401 = false }, 1000)
  }
}

export function isAppealApiPath(url: string | undefined): boolean {
  if (!url) return false
  const path = url.split('?')[0].replace(/\/+$/, '')
  return path.endsWith('/api/v1/appeals') || path.endsWith('/api/v2/appeals')
}

/** Password, passkey, persistent session exchange/revoke, and OAuth failures are not a suspended session. */
export function isCredentialRequest(url: string | undefined): boolean {
  if (!url) return false
  const path = url.split('?')[0]
  return /\/accounts\/(?:login|register|session\/refresh|persistent\/revoke)(?:\/|$)/.test(path)
    || /\/auth\/passkey\/login(?:\/|$)/.test(path)
    || /\/oauth\//.test(path)
}

function explicitRequestBearer(config: InternalAxiosRequestConfig | undefined): string | null {
  const headers = config?.headers
  const fromGetter = typeof headers?.get === 'function' ? headers.get('Authorization') : undefined
  const raw = fromGetter || (headers as { Authorization?: unknown } | undefined)?.Authorization
  if (typeof raw === 'string' && raw.startsWith('Bearer ')) return raw.slice('Bearer '.length)
  return null
}

function requestSessionToken(config: InternalAxiosRequestConfig | undefined): string | null {
  const explicit = explicitRequestBearer(config)
  if (explicit !== null) return explicit
  const tracked = (config as SessionTrackedConfig | undefined)?._sessionToken
  if (tracked !== undefined) return tracked
  return tokenService.get()
}

async function withCrossTabRefreshLock<T>(fn: () => Promise<T>): Promise<T> {
  const locks = typeof navigator !== 'undefined' ? (navigator as Navigator & { locks?: { request?: <R>(name: string, cb: () => Promise<R>) => Promise<R> } }).locks : undefined
  if (typeof locks?.request === 'function') {
    return locks.request('community:persistent-session-refresh', fn)
  }
  return fn()
}

async function performPersistentExchange(initialToken: string): Promise<PersistentRefreshOutcome> {
  return withCrossTabRefreshLock(async () => {
    if (isLocalSignOut()) return 'invalid'
    const currentToken = tokenService.get()
    if (!currentToken) return 'invalid'

    // Another tab already exchanged the expired cookie and rotated the stored
    // persistent credential while this tab waited for the lock.
    if (currentToken !== initialToken) {
      setPendingCookieExchange(false)
      notifyPersistentTokenChange(currentToken)
      return 'refreshed'
    }

    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), requestTimeout())
    try {
      const res = await fetch(`${API_BASE_URL}/api/v1/accounts/session/refresh`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Accept: 'application/json',
        },
        credentials: 'include',
        redirect: 'error',
        body: JSON.stringify({ persistent_token: currentToken }),
        signal: controller.signal,
      })

      if (res.status === 408 || res.status === 429 || res.status >= 500) {
        return 'transient'
      }
      if (!res.ok) {
        const afterFailure = tokenService.get()
        if (afterFailure && afterFailure !== currentToken) {
          setPendingCookieExchange(false)
          notifyPersistentTokenChange(afterFailure)
          return 'refreshed'
        }
        return 'invalid'
      }

      const body = await res.json().catch(() => null) as { persistent_token?: unknown; token?: unknown } | null
      const nextPersistent = typeof body?.persistent_token === 'string' && body.persistent_token !== ''
        ? body.persistent_token
        : typeof body?.token === 'string' && body.token !== ''
          ? body.token
          : ''
      if (!nextPersistent) {
        return 'invalid'
      }

      if (!isLocalSignOut()) {
        const wasAppeal = isAppealSession()
        rotateAccountToken(currentToken, nextPersistent, getActiveAccountId())
        tokenService.set(nextPersistent, { preserveAppeal: wasAppeal })
        setPendingCookieExchange(false)
        notifyPersistentTokenChange(nextPersistent)
      }
      return 'refreshed'
    } catch {
      return 'transient'
    } finally {
      clearTimeout(timer)
    }
  })
}

/**
 * Exchange the active account's encrypted localStorage credential for a fresh
 * HttpOnly `app-token` cookie and rotate the stored persistent credential.
 * Single-flight within the tab and serialized across tabs via Web Locks.
 */
export function refreshSessionWithPersistentToken(expectedToken: string | null = tokenService.get()): Promise<PersistentRefreshOutcome> {
  if (isLocalSignOut()) return Promise.resolve('invalid')
  const current = tokenService.get()
  if (!current) return Promise.resolve('invalid')
  if (expectedToken && expectedToken !== current) {
    return Promise.resolve('refreshed')
  }
  if (!inflightSessionRefresh || inflightSessionRefresh.token !== current) {
    const entry = { token: current, promise: Promise.resolve<PersistentRefreshOutcome>('invalid') }
    entry.promise = performPersistentExchange(current).finally(() => {
      if (inflightSessionRefresh === entry) inflightSessionRefresh = null
    })
    inflightSessionRefresh = entry
  }
  return inflightSessionRefresh.promise
}

async function probeAppeals(): Promise<boolean> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), requestTimeout())
  try {
    const res = await fetch(`${API_BASE_URL}/api/v1/appeals`, {
      headers: { Accept: 'application/json' },
      credentials: 'include',
      signal: controller.signal,
    })
    // A transient failure cannot establish that a session expired.
    if (res.status >= 500 || res.status === 429) throw new Error('Appeal check unavailable')
    return res.ok
  } finally {
    clearTimeout(timer)
  }
}

/**
 * A 401 from the auth middleware means the session is dead or the account is
 * suspended. Suspended accounts can still GET /appeals via their session cookie.
 */
export function retainSuspendedAppealSession(url: string | undefined, token: string | null): Promise<boolean> {
  if (isLocalSignOut() || isCredentialRequest(url) || isAppealApiPath(url)) return Promise.resolve(false)
  if (!token || token !== tokenService.get()) return Promise.resolve(false)
  if (isAppealSession()) return Promise.resolve(true)
  if (!appealProbe || appealProbe.token !== token) {
    const probe = { token, promise: Promise.resolve(false) }
    probe.promise = probeAppeals().then(ok => {
      if (ok && !isLocalSignOut() && token === tokenService.get()) {
        markAppealSession()
        return true
      }
      return false
    }).finally(() => { if (appealProbe === probe) appealProbe = null })
    appealProbe = probe
  }
  return appealProbe.promise
}

// Request interceptor: daily requests authenticate via HttpOnly cookie (`withCredentials: true`).
// Never attach the encrypted persistent localStorage credential as an Authorization header.
client.interceptors.request.use(
  async (config) => {
    // Re-evaluated per request: the connection type changes on mobile.
    if (config.timeout === DEFAULT_TIMEOUT_MS) config.timeout = requestTimeout()

    if (!explicitRequestBearer(config) && !isCredentialRequest(config.url) && hasPendingCookieExchange()) {
      const pendingToken = tokenService.get()
      if (pendingToken) {
        const outcome = await refreshSessionWithPersistentToken(pendingToken)
        if (outcome === 'transient') {
          throw Object.assign(new Error(TIMEOUT_MESSAGE), { code: 'ERR_NETWORK' })
        }
        if (outcome === 'invalid') {
          setPendingCookieExchange(false)
          triggerAuthExpired(pendingToken)
          throw new AxiosError(
            t('shared.client.s4'),
            'ERR_BAD_REQUEST',
            config,
            undefined,
            { status: 401, statusText: 'Unauthorized', headers: {}, config, data: { error: 'unauthorized' } },
          )
        }
      } else {
        setPendingCookieExchange(false)
      }
    }

    if (!explicitRequestBearer(config)) {
      (config as SessionTrackedConfig)._sessionToken = tokenService.get()
    }
    return applyStallRetry(config, client.defaults.adapter)
  },
  (error) => Promise.reject(error),
)

function readApiMessage(data: unknown): string | undefined {
  if (!data) return undefined
  if (data instanceof ArrayBuffer) {
    try {
      const parsed = JSON.parse(new TextDecoder().decode(data)) as { error?: string; message?: string }
      return parsed.error || parsed.message
    } catch {
      return undefined
    }
  }
  if (typeof data === 'object') {
    const body = data as { error?: string; message?: string }
    return body.error || body.message
  }
  return undefined
}

export function isTimeoutError(error: { code?: string, message?: string } | null | undefined): boolean {
  if (!error) return false
  return error.code === 'ECONNABORTED' || error.code === 'ETIMEDOUT' || /timeout/i.test(error.message ?? '')
}

// Enhanced error transform
function transformError(error: AxiosError<ApiErrorData>): Error {
  const status = error.response?.status
  const rawApiMessage = readApiMessage(error.response?.data)
  const apiMessage = translateApiError(rawApiMessage) || rawApiMessage

  if (!error.response) {
    if (isTimeoutError(error)) return Object.assign(new Error(TIMEOUT_MESSAGE), { code: 'ETIMEDOUT' })
    if (typeof navigator !== 'undefined' && navigator.onLine === false) return Object.assign(new Error(OFFLINE_MESSAGE), { code: 'OFFLINE' })
    return Object.assign(new Error(t('shared.client.s3')), { code: 'ERR_NETWORK' })
  }

  const make = (message: string) => Object.assign(new Error(message), { status, apiMessage: rawApiMessage })
  switch (status) {
    case 401:
      return make(apiMessage || t('shared.client.s4'))
    case 403:
      return make(apiMessage || t('shared.client.s5'))
    case 404:
      return make(apiMessage || t('shared.client.s6'))
    case 429:
      return make(apiMessage || t('shared.client.s7'))
    case 500:
    case 502:
    case 503:
      return make(apiMessage || t('shared.client.s8'))
    default:
      return make(apiMessage || error.message || t('shared.client.s9'))
  }
}

// Response interceptor with automatic persistent-token cookie exchange & rotation on 401
client.interceptors.response.use(
  (response) => response,
  async (error: AxiosError<ApiErrorData>) => {
    const gated = (error.response?.data as { code?: string } | undefined)?.code === 'SIGNIN_REQUIRED'
    const config = error.config as SessionTrackedConfig | undefined
    const requestUrl = config?.url
    const explicitBearer = explicitRequestBearer(config)
    const sessionToken = requestSessionToken(config)
    const authRejection = (readApiMessage(error.response?.data) || '').trim().toLowerCase() === 'unauthorized'

    if (
      error.response?.status === 401
      && explicitBearer === null
      && sessionToken !== null
      && sessionToken === tokenService.get()
      && !gated
      && !isLocalSignOut()
      && !isCredentialRequest(requestUrl)
    ) {
      if (isAppealSession() && !isAppealApiPath(requestUrl)) {
        const transformed = transformError(error)
        error.message = transformed.message
        return Promise.reject(error)
      }

      if (config && !config._persistentRetried) {
        const refreshOutcome = await refreshSessionWithPersistentToken(sessionToken)
        if (refreshOutcome === 'transient') {
          return Promise.reject(Object.assign(new Error(TIMEOUT_MESSAGE), { code: 'ERR_NETWORK' }))
        }
        if (refreshOutcome === 'refreshed') {
          config._persistentRetried = true
          config._sessionToken = tokenService.get()
          return client.request(config)
        }
      }

      const activeNow = tokenService.get()
      let keep = false
      if (activeNow && authRejection && !isAppealApiPath(requestUrl)) {
        try {
          keep = await retainSuspendedAppealSession(requestUrl, activeNow)
        } catch {
          return Promise.reject(Object.assign(new Error(TIMEOUT_MESSAGE), { code: 'ERR_NETWORK' }))
        }
      }

      if (!keep) {
        triggerAuthExpired(activeNow)
      }
    }

    const transformed = transformError(error)
    error.message = transformed.message

    return Promise.reject(error)
  },
)

export default client
