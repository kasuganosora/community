import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import type { MastodonAccount } from '@/shared/types/mastodon'
import type { AuthCredentials } from '../../domain/entities'
import { authApi } from '../../infrastructure/authApi'
import { tokenService } from '@/shared/api/client'


import { USE_MOCK } from '@/shared/utils/env'

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(tokenService.get())
  const currentUser = ref<MastodonAccount | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)

  // Version guard for fetchCurrentUser — prevents a stale verify_credentials
  // response from overwriting newer user data
  let fetchUserVersion = 0

  const isAuthenticated = computed(() => !!token.value)

  // Guard against concurrent login attempts — if a second login() is called
  // while the first is still in flight, the second one is rejected.
  let isLoggingIn = false

  async function login(credentials: AuthCredentials) {
    if (isLoggingIn) {
      throw new Error('Login is already in progress')
    }
    isLoggingIn = true
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
        await logout()
        throw new Error('登录后获取用户信息失败')
      }
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Login failed'
      error.value = msg
      throw e
    } finally {
      isLoggingIn = false
      loading.value = false
    }
  }

  async function fetchCurrentUser(): Promise<boolean> {
    const myVersion = ++fetchUserVersion
    if (USE_MOCK) {
      const { mockCurrentUser } = await import('@/shared/api/mockData')
      if (myVersion !== fetchUserVersion) return true
      currentUser.value = mockCurrentUser
      return true
    }
    try {
      const user = await authApi.verifyCredentials()
      if (myVersion !== fetchUserVersion) return true
      currentUser.value = user
      return true
    } catch (e) {
      console.error('Failed to verify credentials:', e)
      if (myVersion !== fetchUserVersion) return false
      currentUser.value = null
      return false
    }
  }

  async function logout(redirectToLogin = false) {
    // Notify backend to invalidate the token — ignore failures to ensure local cleanup always runs
    if (!USE_MOCK) {
      try {
        await authApi.logout()
      } catch (e) {
        console.error('Logout API call failed, proceeding with local cleanup:', e)
      }
    }
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
