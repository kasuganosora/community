import { expect, it } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import AccountRow from '../AccountRow.vue'
import type { MastodonAccount } from '@/shared/types/mastodon'

it('displays a Unicode domain while navigating with the canonical account identifier', async () => {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/', component: { template: '<div />' } },
    { path: '/@:username', component: { template: '<div />' } },
  ] })
  await router.push('/')
  const account = { id: 'remote', username: 'chn', acct: 'chn@xn--s8w913fdga.chn.moe', display_name: 'chn' } as MastodonAccount
  const wrapper = shallowMount(AccountRow, { props: { account }, global: { plugins: [router] } })
  expect(wrapper.find('.user-item__username').text()).toBe('@chn@铜锣湾.chn.moe')
  expect(wrapper.attributes('aria-label')).toContain('@chn@铜锣湾.chn.moe')
  await wrapper.trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/@chn@xn--s8w913fdga.chn.moe')
  expect(account.acct).toBe('chn@xn--s8w913fdga.chn.moe')
  wrapper.unmount()
})
