import { invalidateSessionData } from '@/shared/api/sessionScope'
import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import type { MastodonAccount } from '@/shared/types/mastodon'
import type { AuthCredentials } from '../../domain/entities'
import { authApi } from '../../infrastructure/authApi'
import { tokenService } from '@/shared/api/client'
import { useMediaMode } from '@/shared/composables/useMediaMode'
import { getPasskey } from '@/shared/utils/webauthn'


import { USE_MOCK } from '@/shared/utils/env'
import * as accountRegistry from '@/shared/api/accounts'
import type { StoredAccount } from '@/shared/api/accounts'

/** Full reload: drops every per-user store, cache and stream for the old identity. */
function reloadTo(path?: string) {
  if (path) window.location.assign(path)
  else window.location.reload()
}

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(tokenService.get())
  const currentUser = ref<MastodonAccount | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)

  // Version guard for fetchCurrentUser — prevents a stale verify_credentials
  // response from overwriting newer user data
  let fetchUserVersion = 0

  const isAuthenticated = computed(() => !!token.value)
  const accounts = ref<StoredAccount[]>(accountRegistry.listAccounts())
  function refreshAccounts() { accounts.value = accountRegistry.listAccounts() }

  // Guard against concurrent login attempts — if a second login() is called
  // while the first is still in flight, the second one is rejected.
  let isLoggingIn = false

  async function login(credentials: AuthCredentials) {
    if (isLoggingIn) {
      throw new Error('正在登录，请稍候')
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
      await establishSession(res.token)
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : '登录失败，请检查用户名和密码'
      error.value = msg
      throw e
    } finally {
      isLoggingIn = false
      loading.value = false
    }
  }

  async function establishSession(rawToken: string) {
    invalidateSessionData()
    token.value = rawToken
    tokenService.set(rawToken)
    const success = await fetchCurrentUser()
    if (!success) {
      await logout()
      throw new Error('登录后获取用户信息失败')
    }
  }

  async function loginWithPasskey() {
    if (isLoggingIn) throw new Error('正在登录，请稍候')
    isLoggingIn = true
    loading.value = true
    error.value = null
    try {
      const options = await authApi.passkeyLoginBegin()
      const credential = await getPasskey(options)
      const res = await authApi.passkeyLoginFinish(credential)
      await establishSession(res.token)
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : '通行密钥登录失败'
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
      if (token.value) { accountRegistry.upsertAccount(user, token.value); refreshAccounts() }
      void useMediaMode().loadMode() // adopt the saved media display mode
      return true
    } catch (e) {
      console.error('Failed to verify credentials:', e)
      if (myVersion !== fetchUserVersion) return false
      currentUser.value = null
      return false
    }
  }

  /**
   * Sign out the active account only. If other accounts are signed in on this
   * device, the next usable one becomes active (returns true; caller should
   * reload via the returned promise's side effect when `switchAfter`).
   */
  async function logout(redirectToLogin = false, switchAfter = true): Promise<boolean> {
    ++fetchUserVersion
    invalidateSessionData()
    const leaving = token.value
    // Notify backend to invalidate the token — ignore failures to ensure local cleanup always runs
    if (!USE_MOCK) {
      try {
        await authApi.logout()
      } catch (e) {
        console.error('Logout API call failed, proceeding with local cleanup:', e)
      }
    }
    invalidateSessionData()
    const gone = accountRegistry.findByToken(leaving)
    if (gone) accountRegistry.removeAccount(gone.id)
    refreshAccounts()
    token.value = null
    currentUser.value = null
    tokenService.clear()
    const next = switchAfter ? accountRegistry.nextUsableAccount(gone?.id) : null
    if (next) {
      tokenService.set(next.token)
      reloadTo('/')
      return true
    }
    if (redirectToLogin) {
      window.location.href = '/login'
    }
    return false
  }

  /** Make another signed-in account active. */
  function switchAccount(id: string): 'switched' | 'relogin' | 'noop' {
    const acc = accountRegistry.getAccount(id)
    if (!acc || acc.id === currentUser.value?.id) return 'noop'
    if (acc.needsRelogin || !acc.token) return 'relogin'
    invalidateSessionData()
    tokenService.set(acc.token)
    token.value = acc.token
    reloadTo()
    return 'switched'
  }

  /** Sign out a signed-in account. For the active one this is logout(). */
  async function removeAccount(id: string) {
    if (id === currentUser.value?.id) return logout()
    const acc = accountRegistry.removeAccount(id)
    refreshAccounts()
    if (acc?.token && !USE_MOCK) await authApi.logout(acc.token)
    return false
  }

  /**
   * The active token was rejected (401): keep the account listed as needing
   * sign-in, and fall back to another usable account if there is one.
   * Returns true when it switched (the page reloads).
   */
  function expireSession(): boolean {
    ++fetchUserVersion
    invalidateSessionData()
    const dead = accountRegistry.markNeedsRelogin(token.value ?? '')
    refreshAccounts()
    token.value = null
    currentUser.value = null
    tokenService.clear()
    const next = accountRegistry.nextUsableAccount(dead?.id)
    if (next) {
      tokenService.set(next.token)
      reloadTo('/')
      return true
    }
    return false
  }

  // Another tab switched or signed out: this tab must not keep acting as the
  // old identity with a different token underneath.
  if (typeof window !== 'undefined') {
    window.addEventListener('storage', (e) => {
      if (e.key === 'auth_token' && e.newValue !== token.value) reloadTo()
      else if (e.key === accountRegistry.ACCOUNTS_KEY) refreshAccounts()
    })
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
    accounts,
    refreshAccounts,
    switchAccount,
    removeAccount,
    expireSession,
    login,
    loginWithPasskey,
    logout,
    fetchCurrentUser,
    initialize,
  }
})
