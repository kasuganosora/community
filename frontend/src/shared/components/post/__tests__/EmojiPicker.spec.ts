import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import EmojiPicker from '../EmojiPicker.vue'
import { useEmojiStore } from '@/domains/emoji/presentation/stores/emojiStore'
import { EMOJI_CATALOGUE_STORAGE_KEY } from '@/domains/emoji/domain/emojiCatalogue'
import client from '@/shared/api/client'
import type { MastodonEmoji } from '@/shared/types/mastodon'
const base: MastodonEmoji = { shortcode: 'wave', url: 'https://images.test/wave.png', static_url: '', visible_in_picker: true, aliases: ['hello', '你好'], category: '问候' }
function setup() {
  const pinia = createPinia(), store = useEmojiStore(pinia)
  vi.spyOn(store, 'load').mockResolvedValue()
  store.emojis = [base, { ...base, shortcode: 'secret', aliases: [], is_sensitive: true, category: '其他' }, { ...base, shortcode: 'hidden', visible_in_picker: false }]
  return mount(EmojiPicker, { global: { plugins: [pinia] } })
}
describe('custom emoji picker', () => {
  beforeEach(() => localStorage.clear())
  it('clears searches, previews a focused name and closes with Escape', async () => {
    const wrapper = setup(); await flushPromises()
    await wrapper.get('input[type=search]').setValue('no match')
    expect(wrapper.text()).toContain('没有匹配的表情')
    await wrapper.get('[aria-label="清除表情搜索"]').trigger('click')
    expect(wrapper.findAll('.emoji-picker__grid button')).toHaveLength(2)
    await wrapper.get('.emoji-picker__grid button').trigger('focus')
    expect(wrapper.get('.emoji-picker__preview strong').text()).toBe(':wave:')
    await wrapper.get('section').trigger('keydown', { key: 'Escape' })
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })
  it('searches aliases and filters by category, excludes hidden entries', async () => {
    const wrapper = setup(); await flushPromises()
    expect(wrapper.findAll('.emoji-picker__grid button')).toHaveLength(2)
    await wrapper.get('input[type=search]').setValue('你好')
    expect(wrapper.findAll('.emoji-picker__grid button')).toHaveLength(1)
    await wrapper.get('.emoji-picker__grid button').trigger('click')
    expect(wrapper.emitted('select')?.[0]?.[0]).toMatchObject({ shortcode: 'wave' })
    await wrapper.get('select').setValue('其他')
    expect(wrapper.text()).toContain('没有匹配的表情')
    wrapper.unmount()
  })
  it('does not load sensitive image before explicit opt-in', async () => {
    const wrapper = setup(); await flushPromises()
    expect(wrapper.findAll('img')).toHaveLength(1)
    expect(wrapper.get('button[aria-label="插入 :secret:"]').attributes('disabled')).toBeDefined()
    await wrapper.get('input[type=checkbox]').setValue(true)
    expect(wrapper.findAll('img')).toHaveLength(2)
    expect(wrapper.get('button[aria-label="插入 :secret:"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
})

describe('emoji picker catalogue loading', () => {
  beforeEach(() => localStorage.clear())

  it('shows the fetched emojis once loading finishes', async () => {
    vi.spyOn(client, 'get').mockResolvedValue({ data: [base, { ...base, shortcode: 'blob', aliases: [], category: '其他' }] })
    const wrapper = mount(EmojiPicker, { global: { plugins: [createPinia()] } })
    await flushPromises()
    expect(wrapper.text()).not.toContain('正在加载表情')
    expect(wrapper.findAll('.emoji-picker__grid button')).toHaveLength(2)
    expect(wrapper.text()).toContain('2 个表情')
    wrapper.unmount()
  })

  it('shows an error instead of spinning forever when the catalogue request fails', async () => {
    vi.spyOn(client, 'get').mockRejectedValue(new Error('offline'))
    const wrapper = mount(EmojiPicker, { global: { plugins: [createPinia()] } })
    await flushPromises()
    expect(wrapper.text()).not.toContain('正在加载表情')
    expect(wrapper.text()).toContain('暂时无法加载')
    expect(wrapper.text()).toContain('表情加载失败')
    wrapper.unmount()
  })

  it('paints a warm catalogue immediately and does not replace it when the revision matches', async () => {
    const secret = { ...base, shortcode: 'secret', aliases: ['悄悄话'], is_sensitive: true }
    const hidden = { ...base, shortcode: 'hidden', visible_in_picker: false, aliases: [] }
    localStorage.setItem(EMOJI_CATALOGUE_STORAGE_KEY, JSON.stringify({
      v: 1,
      revision: 'W/"4.4.4.4"',
      emojis: [base, secret, hidden],
    }))
    let release!: (value: unknown) => void
    const get = vi.spyOn(client, 'get').mockImplementation(() => new Promise(resolve => { release = resolve }))
    const pinia = createPinia()
    const wrapper = mount(EmojiPicker, { global: { plugins: [pinia] } })
    expect(wrapper.text()).not.toContain('正在加载表情')
    expect(wrapper.findAll('.emoji-picker__grid button')).toHaveLength(2)
    expect(wrapper.findAll('img')).toHaveLength(1)
    await wrapper.get('input[type=checkbox]').setValue(true)
    expect(wrapper.findAll('img')).toHaveLength(2)
    await wrapper.get('input[type=search]').setValue('悄悄话')
    expect(wrapper.findAll('.emoji-picker__grid button')).toHaveLength(1)
    release({ status: 304, data: [], headers: { etag: 'W/"4.4.4.4"' } })
    await flushPromises()
    expect(get).toHaveBeenCalledWith('/api/v1/custom_emojis', expect.objectContaining({
      headers: expect.objectContaining({ 'If-None-Match': 'W/"4.4.4.4"' }),
    }))
    wrapper.unmount()
    const again = mount(EmojiPicker, { global: { plugins: [pinia] } })
    expect(again.text()).not.toContain('正在加载表情')
    expect(again.findAll('.emoji-picker__grid button')).toHaveLength(2)
    await flushPromises()
    again.unmount()
  })
})
