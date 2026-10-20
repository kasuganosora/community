import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import GroupMessageView from '../GroupMessageView.vue'
import GroupSticker from '../GroupSticker.vue'
import { groupApi } from '../../../infrastructure/groupApi'
import type { GroupMessage } from '../../../domain/group'
import { i18n } from '@/shared/i18n'

vi.mock('../../../infrastructure/groupApi', () => ({ groupApi: { media: vi.fn(), translate: vi.fn() } }))
vi.mock('@/shared/api/client', () => ({ default: { get: vi.fn().mockResolvedValue({ data: { configuration: { translation: { enabled: true, group_messages: true } } } }), patch: vi.fn() } }))

const base: GroupMessage = { id: '$m', sender: { id: '@u:x', display_name: 'User' }, body: 'Привет, как дела?', language: 'ru', kind: 'text', created_at: 1, own: false, redacted: false, edited: false, reactions: [] }
const setup = (overrides: Partial<GroupMessage> = {}, props: Record<string, unknown> = {}) => mount(GroupMessageView, { props: { message: { ...base, ...overrides }, groupId: 'g1', canSend: true, canRedact: false, canTranslate: true, ...props }, global: { stubs: { GroupAvatar: true } } })
const setLocale = (l: string) => { (i18n.global.locale as unknown as { value: string }).value = l }

beforeEach(() => {
  vi.mocked(groupApi.translate).mockReset(); vi.mocked(groupApi.media).mockReset()
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:sticker'), revokeObjectURL: vi.fn() }))
  setLocale('en')
})

describe('group message translation', () => {
  it('sits beside Reply and toggles translation and original with a loading state', async () => {
    let resolve!: (v: unknown) => void
    vi.mocked(groupApi.translate).mockReturnValue(new Promise(r => { resolve = r }) as never)
    const w = setup(); await flushPromises()
    const buttons = w.findAll('.group-message__actions button').map(b => b.text())
    expect(buttons.slice(1, 3)).toEqual([expect.any(String), 'Translate'])
    const btn = w.get('.group-message__translate')
    await btn.trigger('click')
    expect(btn.text()).toBe('Translating…'); expect(btn.attributes('aria-busy')).toBe('true')
    expect(groupApi.translate).toHaveBeenCalledWith('g1', '$m', 'en')
    resolve({ content: '<p>Hi, how are you?</p>', detected_source_language: 'ru', language: 'en', provider: 'mt-gateway', engine: 'traditional' }); await flushPromises()
    expect(w.get('.group-message__body').text()).toBe('Hi, how are you?')
    expect(btn.text()).toBe('Show original')
    await btn.trigger('click')
    expect(w.get('.group-message__body').text()).toBe('Привет, как дела?')
    await btn.trigger('click'); expect(groupApi.translate).toHaveBeenCalledTimes(1)
  })
  it('hides the button when already in the UI language, not configured, or not allowed', async () => {
    expect(setup({ language: 'en', body: 'Hello there' }).find('.group-message__translate').exists()).toBe(false)
    expect(setup({ language: undefined }).find('.group-message__translate').exists()).toBe(false)
    expect(setup({}, { canTranslate: false }).find('.group-message__translate').exists()).toBe(false)
    expect(setup({ redacted: true }).find('.group-message__translate').exists()).toBe(false)
  })
  it('localises errors including the group visibility restriction', async () => {
    vi.mocked(groupApi.translate).mockRejectedValue(Object.assign(new Error('x'), { isAxiosError: true, response: { status: 403, data: { error_code: 'translation_visibility_restricted' } } }))
    const w = setup(); await flushPromises()
    await w.get('.group-message__translate').trigger('click'); await flushPromises()
    expect(w.get('.group-message__translate-error').text()).toBe('Messages of this group cannot be translated on this server')
  })
  it('uses the zh UI locale as the target', async () => {
    setLocale('zh-CN')
    vi.mocked(groupApi.translate).mockResolvedValue({ content: '<p>你好</p>', detected_source_language: 'ru', language: 'zh-CN', provider: 'p', engine: 'llm' })
    const w = setup(); await flushPromises()
    expect(w.get('.group-message__translate').text()).toBe('翻译')
    await w.get('.group-message__translate').trigger('click'); await flushPromises()
    expect(groupApi.translate).toHaveBeenCalledWith('g1', '$m', 'zh-CN')
  })
})

describe('group stickers', () => {
  const sticker = (type: string) => ({ kind: 'sticker' as const, body: '(weary face)', language: undefined, attachments: [{ id: 's', name: 'sticker', type, size: 7000, url: '/api/v1/chat/groups/g1/messages/%24m/media/s' }] })
  const webm = new Blob([new Uint8Array([0x1a, 0x45, 0xdf, 0xa3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0])])
  const webp = new Blob([new Uint8Array([0x52, 0x49, 0x46, 0x46, 0, 0, 0, 0, 0x57, 0x45, 0x42, 0x50, 0, 0, 0, 0])])
  it('renders webm inline as a muted looping video without a download card or duplicate alt text', async () => {
    vi.mocked(groupApi.media).mockResolvedValue(webm)
    const w = setup(sticker('video/webm')); await flushPromises(); await flushPromises()
    const video = w.get('.group-sticker video')
    expect(video.attributes()).toMatchObject({ loop: '', playsinline: '' })
    expect((video.element as HTMLVideoElement).muted).toBe(true)
    expect(w.get('.group-sticker').attributes('title')).toBe('(weary face)')
    expect(w.find('.group-attachment').exists()).toBe(false)
    expect(w.find('.group-message__body').exists()).toBe(false)
    expect(w.text()).not.toContain('KB')
  })
  it('renders webp as an image and falls back to alt text for unknown bytes', async () => {
    vi.mocked(groupApi.media).mockResolvedValue(webp)
    const w = setup(sticker('image/webp')); await flushPromises(); await flushPromises()
    expect(w.get('.group-sticker img').attributes('src')).toBe('blob:sticker')
    vi.mocked(groupApi.media).mockResolvedValue(new Blob(['<html>']))
    const f = mount(GroupSticker, { props: { attachment: sticker('image/webp').attachments[0]!, alt: '(weary face)' } }); await flushPromises(); await flushPromises()
    expect(f.get('.group-sticker__fallback').text()).toBe('(weary face)')
  })
  it('pauses animation when reduced motion is preferred', async () => {
    vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() })))
    vi.mocked(groupApi.media).mockResolvedValue(webm)
    const w = mount(GroupSticker, { props: { attachment: sticker('video/webm').attachments[0]!, alt: 'x' } }); await flushPromises(); await flushPromises()
    expect(w.get('video').attributes('autoplay')).toBeUndefined()
    vi.unstubAllGlobals()
  })
})
