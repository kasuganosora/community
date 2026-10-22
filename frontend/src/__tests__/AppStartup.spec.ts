import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
const initialize = vi.hoisted(() => vi.fn())
vi.mock('@/domains/auth/presentation/stores/authStore', () => ({ useAuthStore: () => ({ initialize }) }))
vi.mock('@/shared/composables/useAccountHover', () => ({ accountHover: null }))
import { t } from '@/shared/i18n'
import App from '../App.vue'

describe('startup on slow networks', () => {
  afterEach(() => { vi.useRealTimers(); vi.clearAllMocks() })
  const options = { global: { stubs: { SlowNetworkHint: true, TLoading: true, RouterView: { template: '<main>Loaded</main>' } } } }

  it('recovers from an initialization timeout without reloading the app', async () => {
    initialize.mockRejectedValueOnce(new Error('timeout')).mockResolvedValueOnce(true)
    const wrapper = mount(App, options)
    await flushPromises()
    expect(wrapper.get('.app-init-error').text()).toContain('timeout')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(initialize).toHaveBeenCalledTimes(2)
    expect(wrapper.get('main').text()).toBe('Loaded')
    wrapper.unmount()
  })

  it('explains a slow startup and clears the hint after a successful response', async () => {
    vi.useFakeTimers()
    let finish!: (result: boolean) => void
    initialize.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mount(App, options)
    await vi.advanceTimersByTimeAsync(6000)
    expect(wrapper.get('[role="status"]').text()).toContain(t('shared.App.s4'))
    finish(true)
    await flushPromises()
    expect(wrapper.get('main').text()).toBe('Loaded')
    wrapper.unmount()
  })
})
