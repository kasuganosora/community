import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import type { MastodonAccount } from '@/shared/types/mastodon'
import type { AuthCredentials } from '../../domain/entities'
import { authApi } from '../../infrastructure/authApi'
import { mockCurrentUser } from '@/shared/api/mockData'

const USE_MOCK = !import.meta.env.VITE_API_URL

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(localStorage.getItem('auth_token'))
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
        localStorage.setItem('auth_token', 'mock-token')
        currentUser.value = mockCurrentUser
        return
      }
      const res = await authApi.login(credentials)
      token.value = res.token
      localStorage.setItem('auth_token', res.token)
      await fetchCurrentUser()
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Login failed'
      error.value = msg
      throw e
    } finally {
      loading.value = false
    }
  }

  async function fetchCurrentUser() {
    if (USE_MOCK) {
      currentUser.value = mockCurrentUser
      return
    }
    try {
      currentUser.value = await authApi.verifyCredentials()
    } catch {
      logout()
    }
  }

  function logout() {
    token.value = null
    currentUser.value = null
    localStorage.removeItem('auth_token')
  }

  async function initialize() {
    if (token.value) {
      await fetchCurrentUser()
    }
  }

  return { token, currentUser, loading, error, isAuthenticated, login, logout, fetchCurrentUser, initialize }
})
