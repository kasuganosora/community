import axios from 'axios'
import type { AxiosError } from 'axios'
import type { ApiErrorData } from '@/shared/types/mastodon'

// Constants
export const AUTH_TOKEN_KEY = 'auth_token'
export const API_BASE_URL = import.meta.env.VITE_API_URL || ''

const client = axios.create({
  baseURL: API_BASE_URL,
  timeout: 15000,
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

// Request interceptor
client.interceptors.request.use(
  (config) => {
    const token = tokenService.get()
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => Promise.reject(error)
)

// Enhanced error transform
function transformError(error: AxiosError<ApiErrorData>): Error {
  const status = error.response?.status
  const apiMessage = error.response?.data?.error || error.response?.data?.message

  if (!error.response) {
    // Network error
    return new Error('网络连接失败，请检查网络设置')
  }

  switch (status) {
    case 401:
      return new Error(apiMessage || '登录已过期，请重新登录')
    case 403:
      return new Error(apiMessage || '没有权限执行此操作')
    case 404:
      return new Error(apiMessage || '请求的资源不存在')
    case 429:
      return new Error(apiMessage || '操作过于频繁，请稍后再试')
    case 500:
    case 502:
    case 503:
      return new Error(apiMessage || '服务器暂时不可用，请稍后重试')
    default:
      return new Error(apiMessage || error.message || '请求失败')
  }
}

// Response interceptor with improved error handling
client.interceptors.response.use(
  (response) => response,
  (error: AxiosError<ApiErrorData>) => {
    // Handle 401 — use callback instead of hard redirect
    if (error.response?.status === 401) {
      const currentPath = window.location.pathname
      if (!currentPath.includes('/login') && !currentPath.includes('/register')) {
        // Call callback first — it may want to use the token for refresh
        if (authExpiredCallback) {
          authExpiredCallback()
        }
        // Clear token after callback so it can still access old token if needed
        tokenService.clear()
        // Fallback: hard redirect only if no callback set
        if (!authExpiredCallback) {
          window.location.href = '/login'
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
