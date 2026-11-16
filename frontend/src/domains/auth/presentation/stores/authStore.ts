import { invalidateSessionData } from '@/shared/api/sessionScope'
import { ref, computed, onScopeDispose } from 'vue'
import { defineStore } from 'pinia'
import type { MastodonAccount } from '@/shared/types/mastodon'
import type { AuthCredentials, LoginResponse } from '../../domain/entities'
import { authApi } from '../../infrastructure/authApi'
import { beginLocalSignOut, endLocalSignOut, isAppealSession } from '@/shared/api/appealSession'
import { tokenService } from '@/shared/api/client'
import { useMediaMode } from '@/shared/composables/useMediaMode'
import { syncUiLocale } from '@/shared/composables/useUiLocale'
import { getPasskey } from '@/shared/utils/webauthn'

import { USE_MOCK } from '@/shared/utils/env'
import * as accountRegistry from '@/shared/api/accounts'
import type { StoredAccount } from '@/shared/api/accounts'

import { t } from '@/shared/i18n'

/** Full reload: drops every per-user store, cache and stream for the old identity. */
function reloadTo(path?: string) {
  if (path) window.location.assign(path)
  else window.location.reload()
}

function persistentCredentialOf(res: LoginResponse): string {
  return res.persistent_token || res.token
}

let activeStorageHandler: ((e: StorageEvent) => void) | null = null

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

  // Sync in-memory state whenever the API client rotates the active persistent credential.
  const stopTokenSync = accountRegistry.onPersistentTokenChange((nextToken) => {
    token.value = nextToken
    refreshAccounts()
  })

  // Guard against concurrent login attempts — if a second login() is called
  // while the first is still in flight, the second one is rejected.
  let isLoggingIn = false

  async function login(credentials: AuthCredentials) {
    if (isLoggingIn) {
      throw new Error(t('auth.authStore.s1'))
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
      if ('twoFactorRequired' in res) return res
      await establishSession(persistentCredentialOf(res))
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : t('auth.authStore.s2')
      error.value = msg
      throw e
    } finally {
      isLoggingIn = false
      loading.value = false
    }
  }

  /** Finish a password login with a TOTP or backup code. */
  async function loginTwoFactor(challenge: string, code: string) {
    if (isLoggingIn) throw new Error(t('auth.authStore.s1'))
    isLoggingIn = true
    loading.value = true
    error.value = null
    try {
      const res = await authApi.loginTwoFactor(challenge, code)
      await establishSession(persistentCredentialOf(res))
    } catch (e: unknown) {
      error.value = e instanceof Error ? e.message : t('auth.authStore.s2')
      throw e
    } finally {
      isLoggingIn = false
      loading.value = false
    }
  }

  async function establishSession(persistentCredential: string) {
    invalidateSessionData()
    accountRegistry.setPendingCookieExchange(false)
    token.value = persistentCredential
    tokenService.set(persistentCredential)
    const success = await fetchCurrentUser()
    if (!success) {
      if (isAppealSession()) return
      await logout()
      throw new Error(t('auth.authStore.s3'))
    }
  }

  async function loginWithPasskey() {
    if (isLoggingIn) throw new Error(t('auth.authStore.s1'))
    isLoggingIn = true
    loading.value = true
    error.value = null
    try {
      const options = await authApi.passkeyLoginBegin()
      const credential = await getPasskey(options)
      const res = await authApi.passkeyLoginFinish(credential)
      await establishSession(persistentCredentialOf(res))
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : t('auth.authStore.s4')
      error.value = msg
      throw e
    } finally {
      isLoggingIn = false
      loading.value = false
    }
  }

  /** True when a verify_credentials failure may succeed on retry. */
  function isTransientVerifyFailure(e: unknown): boolean {
    const status = (e as { response?: { status?: number } } | null)?.response?.status
    if (typeof status !== 'number') return true // no response: network error / timeout
    return status === 408 || status === 429 || status >= 500
  }

  async function fetchCurrentUser({ throwOnError = false } = {}): Promise<boolean> {
    const myVersion = ++fetchUserVersion
    if (USE_MOCK) {
      const { mockCurrentUser } = await import('@/shared/api/mockData')
      if (myVersion !== fetchUserVersion) return true
      currentUser.value = mockCurrentUser
      return true
    }
    try {
      let user = await authApi.verifyCredentials()
      if (myVersion !== fetchUserVersion) return true

      // Defense-in-depth: if the browser's app-token cookie belonged to a
      // different account than the active persistent credential in localStorage,
      // exchange the persistent credential so the cookie matches before saving.
      const expectedAccount = accountRegistry.findByToken(token.value)
      if (expectedAccount && expectedAccount.id !== user.id && token.value && typeof authApi.refreshPersistentSession === 'function') {
        const refreshed = await authApi.refreshPersistentSession(token.value)
        const nextCred = persistentCredentialOf(refreshed)
        accountRegistry.rotateAccountToken(token.value, nextCred, expectedAccount.id)
        tokenService.set(nextCred)
        token.value = nextCred
        accountRegistry.setPendingCookieExchange(false)
        user = await authApi.verifyCredentials()
        if (myVersion !== fetchUserVersion) return true
      }

      currentUser.value = user
      accountRegistry.setActiveAccountId(user.id)
      if (token.value) {
        accountRegistry.upsertAccount(user, token.value)
        refreshAccounts()
      }
      void useMediaMode().loadMode() // adopt the saved media display mode
      void syncUiLocale() // adopt (or upload) the web UI language
      return true
    } catch (e) {
      console.error('Failed to verify credentials:', e)
      if (myVersion !== fetchUserVersion) return false
      // No response (offline/timeout), 408, 429 or 5xx says nothing about the
      // credential's validity. Keep the session (and any loaded profile) so the
      // caller can offer a retry.
      if (isTransientVerifyFailure(e)) {
        if (throwOnError) throw e
        return false
      }
      // A suspended account keeps the credential: only the appeal routes still work.
      if (isAppealSession()) return false
      // Any other 4xx (401, 403, 422, ...) means the server rejected this
      // session. Sign out locally — the credential is unusable, so no logout API call.
      const rejected = token.value
      currentUser.value = null
      if (rejected) {
        invalidateSessionData()
        accountRegistry.markNeedsRelogin(rejected)
        accountRegistry.setActiveAccountId(null)
        accountRegistry.setPendingCookieExchange(false)
        refreshAccounts()
        token.value = null
        tokenService.clear()
      }
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
    beginLocalSignOut()
    invalidateSessionData()
    const leaving = token.value
    const gone = accountRegistry.findByToken(leaving)
      ?? (currentUser.value ? accountRegistry.getAccount(currentUser.value.id) : null)

    // Notify backend to invalidate the session and persistent credential — ignore failures
    if (!USE_MOCK) {
      try {
        if (leaving && leaving.startsWith('pl1.')) {
          await authApi.logout(undefined, leaving)
        } else {
          await authApi.logout()
        }
      } catch (e) {
        console.error('Logout API call failed, proceeding with local cleanup:', e)
      }
    }
    invalidateSessionData()
    if (gone) accountRegistry.removeAccount(gone.id)
    refreshAccounts()
    token.value = null
    currentUser.value = null
    tokenService.clear()
    const next = switchAfter ? accountRegistry.nextUsableAccount(gone?.id) : null
    if (next) {
      accountRegistry.setActiveAccountId(next.id)
      accountRegistry.setPendingCookieExchange(true)
      tokenService.set(next.token)
      reloadTo('/')
      return true
    }
    accountRegistry.setActiveAccountId(null)
    accountRegistry.setPendingCookieExchange(false)
    if (redirectToLogin) window.location.href = '/login'
    endLocalSignOut()
    return false
  }

  /** Make another signed-in account active and mark its cookie for exchange on reload. */
  function switchAccount(id: string): 'switched' | 'relogin' | 'noop' {
    const acc = accountRegistry.getAccount(id)
    if (!acc || acc.id === currentUser.value?.id) return 'noop'
    if (acc.needsRelogin || !acc.token) return 'relogin'
    invalidateSessionData()
    accountRegistry.setActiveAccountId(acc.id)
    accountRegistry.setPendingCookieExchange(true)
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
   * The active credential was rejected (401): keep the account listed as needing
   * sign-in, and fall back to another usable account if there is one.
   * Returns true when it switched (the page reloads).
   */
  function expireSession(): boolean {
    ++fetchUserVersion
    beginLocalSignOut()
    invalidateSessionData()
    const dead = accountRegistry.markNeedsRelogin(token.value ?? '')
    refreshAccounts()
    token.value = null
    currentUser.value = null
    tokenService.clear()
    const next = accountRegistry.nextUsableAccount(dead?.id)
    if (next) {
      accountRegistry.setActiveAccountId(next.id)
      accountRegistry.setPendingCookieExchange(true)
      tokenService.set(next.token)
      endLocalSignOut()
      reloadTo('/')
      return true
    }
    accountRegistry.setActiveAccountId(null)
    accountRegistry.setPendingCookieExchange(false)
    endLocalSignOut()
    return false
  }

  // Another tab rotated, switched, or signed out:
  // - If the same active account simply rotated its persistent credential,
  //   adopt the new credential in-place without reloading the tab.
  // - If another tab switched to a different account or signed out, reload.
  const onStorage = (e: StorageEvent) => {
    if (e.key === 'auth_token' && e.newValue !== token.value) {
      if (e.newValue && currentUser.value) {
        const owner = accountRegistry.findByToken(e.newValue)
        const activeId = accountRegistry.getActiveAccountId()
        if ((owner && owner.id === currentUser.value.id) || (!owner && activeId === currentUser.value.id)) {
          token.value = e.newValue
          refreshAccounts()
          return
        }
      }
      reloadTo()
    } else if (e.key === accountRegistry.ACCOUNTS_KEY) {
      refreshAccounts()
    }
  }

  if (typeof window !== 'undefined') {
    if (activeStorageHandler) {
      window.removeEventListener('storage', activeStorageHandler)
    }
    activeStorageHandler = onStorage
    window.addEventListener('storage', onStorage)
  }

  onScopeDispose(() => {
    stopTokenSync()
    if (typeof window !== 'undefined' && activeStorageHandler === onStorage) {
      window.removeEventListener('storage', onStorage)
      activeStorageHandler = null
    }
  })

  async function initialize(): Promise<boolean> {
    const stored = tokenService.get()
    if (stored !== token.value) {
      token.value = stored
    }
    if (!token.value) {
      return false
    }
    const initialToken = token.value
    const success = await fetchCurrentUser({ throwOnError: true })
    if (!success && token.value === initialToken && !isAppealSession()) {
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
    loginTwoFactor,
    logout,
    fetchCurrentUser,
    initialize,
  }
})
