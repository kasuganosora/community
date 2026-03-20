import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import type { MastodonAccount } from '@/shared/types/mastodon'
import type { AuthCredentials } from '../../domain/entities'
import { authApi } from '../../infrastructure/authApi'
import { mockCurrentUser } from '@/shared/api/mockData'
import { AUTH_TOKEN_KEY } from '@/shared/api/client'

const USE_MOCK = !import.meta.env.VITE_API_URL

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(localStorage.getItem(AUTH_TOKEN_KEY))
  const currentUser = ref<MastodonAccount | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)

  const isAuthenticated = computed(() => !!token.value)

  async function login(credentials: AuthCredentials) {
    loading.value = true
    error.value = null
    try {
      if (USE_MOCK) {
        token.value = 'mock-token'
        localStorage.setItem(AUTH_TOKEN_KEY, 'mock-token')
        currentUser.value = mockCurrentUser
        return
      }
      const res = await authApi.login(credentials)
      token.value = res.token
      localStorage.setItem(AUTH_TOKEN_KEY, res.token)
      await fetchCurrentUser()
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Login failed'
      error.value = msg
      throw e
    } finally {
      loading.value = false
    }
  }

  async function fetchCurrentUser(): Promise<boolean> {
    if (USE_MOCK) {
      currentUser.value = mockCurrentUser
      return true
    }
    try {
      currentUser.value = await authApi.verifyCredentials()
      return true
    } catch (e) {
      currentUser.value = null
      return false
    }
  }

  function logout(redirectToLogin = false) {
    token.value = null
    currentUser.value = null
    localStorage.removeItem(AUTH_TOKEN_KEY)
    if (redirectToLogin) {
      window.location.href = '/login'
    }
  }

  async function initialize(): Promise<boolean> {
    if (!token.value) {
      return false
    }
    const success = await fetchCurrentUser()
    if (!success) {
      token.value = null
      localStorage.removeItem(AUTH_TOKEN_KEY)
    }
    return success
  }

  return { token, currentUser, loading, error, isAuthenticated, login, logout, fetchCurrentUser, initialize }
})
