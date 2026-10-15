import { describe, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import ProfilePage from '../ProfilePage.vue'
import { userApi } from '@/domains/user/infrastructure/userApi'
import type { MastodonAccount, MastodonRelationship } from '@/shared/types/mastodon'

vi.mock('vue-router', () => ({ useRoute: () => ({ params: { username: 'locked' } }) }))

describe('locked profile follow requests', () => {
  it('preserves follower count when sending or cancelling a pending request', async () => {
    const account = { id: 'locked', username: 'locked', acct: 'locked', locked: true, followers_count: 7 } as MastodonAccount
    const rel = { id: 'locked', following: false, requested: false } as MastodonRelationship
    vi.spyOn(userApi, 'lookupAccount').mockResolvedValue(account)
    vi.spyOn(userApi, 'getStatuses').mockResolvedValue([])
    vi.spyOn(userApi, 'getRelationships').mockResolvedValue([rel])
    vi.spyOn(userApi, 'follow').mockResolvedValue({ ...rel, requested: true })
    vi.spyOn(userApi, 'unfollow').mockResolvedValue(rel)
    const wrapper = shallowMount(ProfilePage, { global: { plugins: [createPinia()] } })
    await flushPromises()
    wrapper.findComponent({ name: 'AppButton' }).vm.$emit('click')
    await flushPromises()
    expect(account.followers_count).toBe(7)
    wrapper.findComponent({ name: 'AppButton' }).vm.$emit('click')
    await flushPromises()
    expect(account.followers_count).toBe(7)
    expect(userApi.unfollow).toHaveBeenCalledWith('locked')
    wrapper.unmount()
  })
})
