import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const get = vi.fn()
vi.mock('@/shared/api/client', () => ({ default: { get: (...args: unknown[]) => get(...args) } }))

describe('BrandMark', () => {
  beforeEach(() => {
    vi.resetModules()
    get.mockReset()
    document.head.innerHTML = '<meta name="theme-color" content="#ffffff">'
  })

  async function render(iconUrl: string) {
    get.mockResolvedValue({ data: { branding: { iconUrl } } })
    const { loadBranding } = await import('@/shared/composables/useBranding')
    await loadBranding(true)
    const { default: BrandMark } = await import('../BrandMark.vue')
    return mount(BrandMark, { props: { size: 32 } })
  }

  it('uses branding.iconUrl when the instance has one', async () => {
    const wrapper = await render('https://files.example/icon.webp')
    await flushPromises()
    const img = wrapper.get('img')
    expect(img.attributes('src')).toBe('https://files.example/icon.webp')
    expect(img.attributes('width')).toBe('32')
    expect(wrapper.find('svg').exists()).toBe(false)
    wrapper.unmount()
  })

  it('falls back to the default mark when iconUrl is empty or the image fails', async () => {
    const empty = await render('')
    await flushPromises()
    expect(empty.find('img').exists()).toBe(false)
    expect(empty.find('svg').exists()).toBe(true)
    empty.unmount()

    const wrapper = await render('https://files.example/missing.webp')
    await flushPromises()
    await wrapper.get('img').trigger('error')
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.find('svg').exists()).toBe(true)
    wrapper.unmount()
  })
})
