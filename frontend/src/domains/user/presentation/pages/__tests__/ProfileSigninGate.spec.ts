import { describe, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import ProfilePage from '../ProfilePage.vue'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { tokenService } from '@/shared/api/client'

vi.mock('vue-router', () => ({ useRoute: () => ({ params: { username: 'quiet' }, fullPath: '/@quiet' }) }))

const quiet = { id: 'quiet', username: 'quiet', acct: 'quiet', followers_count: 1, require_signin_to_view: true } as MastodonAccount

describe('require sign-in to view', () => {
  it('shows the profile but no posts to visitors', async () => {
    vi.spyOn(userApi, 'lookupAccount').mockResolvedValue(quiet)
    const statuses = vi.spyOn(userApi, 'getStatuses').mockResolvedValue([])
    tokenService.clear()
    const pinia = createPinia()
    const wrapper = shallowMount(ProfilePage, { global: { plugins: [pinia], stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    expect(wrapper.find('[data-testid="profile-signin-required"]').exists()).toBe(true)
    expect(statuses).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('loads posts for signed-in users', async () => {
    vi.spyOn(userApi, 'lookupAccount').mockResolvedValue(quiet)
    const statuses = vi.spyOn(userApi, 'getStatuses').mockResolvedValue([])
    vi.spyOn(userApi, 'getRelationships').mockResolvedValue([])
    tokenService.set('token')
    const pinia = createPinia()
    const auth = useAuthStore(pinia)
    auth.currentUser = { id: 'viewer', username: 'viewer', acct: 'viewer' } as MastodonAccount
    const wrapper = shallowMount(ProfilePage, { global: { plugins: [pinia] } })
    await flushPromises()
    expect(wrapper.find('[data-testid="profile-signin-required"]').exists()).toBe(false)
    expect(statuses).toHaveBeenCalled()
    wrapper.unmount()
    tokenService.clear()
  })
})
