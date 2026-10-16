import axios from 'axios'
import type { AxiosError } from 'axios'
import type { ApiErrorData } from '@/shared/types/mastodon'
import { applyStallRetry } from './stallRetry'

import { t } from '@/shared/i18n'
import { translateApiError } from '@/shared/i18n/apiErrors'
// Constants
export const AUTH_TOKEN_KEY = 'auth_token'
export const API_BASE_URL = import.meta.env.VITE_API_URL || ''

// Request timeout. Slow links (2G/3G or Data Saver, per the Network
// Information API where available) get more time: a 20-item remote timeline
// can take well over 15 s on such a connection.
export const DEFAULT_TIMEOUT_MS = 15000
export const SLOW_NETWORK_TIMEOUT_MS = 45000
type NetworkInformationLike = { effectiveType?: string, saveData?: boolean, rtt?: number, downlink?: number }
export function requestTimeout(conn: NetworkInformationLike | undefined = (globalThis.navigator as unknown as { connection?: NetworkInformationLike } | undefined)?.connection): number {
  if (!conn) return DEFAULT_TIMEOUT_MS
  const slow = conn.saveData === true
    || ['slow-2g', '2g', '3g'].includes(conn.effectiveType ?? '')
    || (typeof conn.rtt === 'number' && conn.rtt >= 400)
    || (typeof conn.downlink === 'number' && conn.downlink > 0 && conn.downlink < 1)
  return slow ? SLOW_NETWORK_TIMEOUT_MS : DEFAULT_TIMEOUT_MS
}

const client = axios.create({
  baseURL: API_BASE_URL,
  timeout: DEFAULT_TIMEOUT_MS,
  headers: {
    'Content-Type': 'application/json',
  },
})

// --- Token management: centralized to avoid scattered localStorage access ---
export const tokenService = {
  get(): string | null {
    try {
      return localStorage.getItem(AUTH_TOKEN_KEY)
    } catch {
      return null
    }
  },
  set(token: string): void {
    try {
      localStorage.setItem(AUTH_TOKEN_KEY, token)
    } catch {
      // storage might be full or disabled
    }
  },
  clear(): void {
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

// Request interceptor
client.interceptors.request.use(
  (config) => {
    // Re-evaluated per request: the connection type changes on mobile.
    if (config.timeout === DEFAULT_TIMEOUT_MS) config.timeout = requestTimeout()
    const token = tokenService.get()
    // A caller may target a specific (non-active) account, e.g. revoking a
    // signed-in account from 管理账户; keep its explicit header.
    if (token && !config.headers.Authorization) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return applyStallRetry(config, client.defaults.adapter)
  },
  (error) => Promise.reject(error)
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

export const TIMEOUT_MESSAGE = t('shared.client.s1')
export const OFFLINE_MESSAGE = t('shared.client.s2')

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
    // A timeout on a slow link is not "offline": say so, so users retry
    // instead of checking their settings.
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

// Response interceptor with improved error handling
client.interceptors.response.use(
  (response) => response,
  (error: AxiosError<ApiErrorData>) => {
    // Handle 401 — use callback instead of hard redirect (with reentrancy guard)
    if (error.response?.status === 401 && !isHandling401) {
      const currentPath = window.location.pathname
      if (!currentPath.includes('/login') && !currentPath.includes('/register')) {
        isHandling401 = true
        try {
          // Call callback first — it may want to use the token for refresh
          if (authExpiredCallback) {
            try {
              authExpiredCallback()
            } catch (cbErr) {
              console.error('Auth expired callback failed:', cbErr)
            }
          }
          // Clear token after callback so it can still access old token if needed
          tokenService.clear()
          // Fallback: hard redirect only if no callback set
          if (!authExpiredCallback) {
            window.location.href = '/login'
          }
        } finally {
          // Reset guard after a short delay to allow navigation to complete
          setTimeout(() => { isHandling401 = false }, 1000)
        }
      }
    }

    // Transform error to user-friendly message
    const transformed = transformError(error)
    error.message = transformed.message

    return Promise.reject(error)
  },
)

export default client
