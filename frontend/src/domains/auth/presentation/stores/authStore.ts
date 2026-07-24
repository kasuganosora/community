import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import type { MastodonAccount } from '@/shared/types/mastodon'
import type { AuthCredentials } from '../../domain/entities'
import { authApi } from '../../infrastructure/authApi'
import { tokenService } from '@/shared/api/client'

// Centralized feature flag — only use mock in dev when explicitly enabled
const USE_MOCK = import.meta.env.DEV && !import.meta.env.VITE_API_URL

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(tokenService.get())
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
        tokenService.set('mock-token')
        const { mockCurrentUser } = await import('@/shared/api/mockData')
        currentUser.value = mockCurrentUser
        return
      }
      const res = await authApi.login(credentials)
      token.value = res.token
      tokenService.set(res.token)

      // Verify that we can actually fetch the user — if not, roll back
      const success = await fetchCurrentUser()
      if (!success) {
        logout()
        throw new Error('登录后获取用户信息失败')
      }
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
      const { mockCurrentUser } = await import('@/shared/api/mockData')
      currentUser.value = mockCurrentUser
      return true
    }
    try {
      currentUser.value = await authApi.verifyCredentials()
      return true
    } catch (e) {
      console.error('Failed to verify credentials:', e)
      currentUser.value = null
      return false
    }
  }

  function logout(redirectToLogin = false) {
    token.value = null
    currentUser.value = null
    tokenService.clear()
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
      tokenService.clear()
    }
    return success
  }

  // Only expose token as readonly — components shouldn't mutate it directly
  return {
    token: computed(() => token.value),
    currentUser,
    loading,
    error,
    isAuthenticated,
    login,
    logout,
    fetchCurrentUser,
    initialize,
  }
})
