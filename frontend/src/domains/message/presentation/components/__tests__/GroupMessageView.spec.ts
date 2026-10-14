import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import GroupMessageView from '../GroupMessageView.vue'
import GroupAttachment from '../GroupAttachment.vue'
import { groupApi } from '../../../infrastructure/groupApi'
import type { GroupMessage } from '../../../domain/group'
import { useMediaMode } from '@/shared/composables/useMediaMode'
vi.mock('../../../infrastructure/groupApi', () => ({ groupApi: { media: vi.fn() } }))
vi.mock('@/shared/api/client', () => ({ default: { patch: vi.fn().mockResolvedValue({ data: {} }) } }))
const emojiURL = '/api/v1/chat/groups/g/messages/m/media/emoji'
const message: GroupMessage = { id: 'm', sender: { id: 'u', display_name: 'User' }, body: 'hello :wave:', kind: 'text', created_at: 1, own: false, redacted: false, edited: false, reactions: [] }
const setup = (overrides: Partial<GroupMessage> = {}) => mount(GroupMessageView, { props: { message: { ...message, ...overrides }, canSend: true, canRedact: false }, global: { stubs: { GroupAvatar: true } } })
beforeEach(async () => { vi.resetAllMocks(); await useMediaMode().setMode('default'); vi.mocked(groupApi.media).mockResolvedValue(new Blob(['image'], { type: 'image/png' })); vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:trusted-image'), revokeObjectURL: vi.fn() })) })
describe('private group message rendering', () => {
  it('loads custom emoji through authenticated media and never renders untrusted remote images', async () => {
    const wrapper = setup({ formatted_body: 'hello <img src="https://tracker.invalid/pixel" alt=":wave:" onerror="alert(1)">', custom_emojis: [{ shortcode: 'wave', url: emojiURL, static_url: emojiURL }] })
    await flushPromises()
    expect(groupApi.media).toHaveBeenCalledWith(emojiURL, expect.any(AbortSignal))
    expect(wrapper.get('.group-message__body img').attributes('src')).toBe('blob:trusted-image')
    expect(wrapper.html()).not.toContain('tracker.invalid'); expect(wrapper.html()).not.toContain('onerror')
    wrapper.unmount(); expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:trusted-image')
  })
  it('shows readable custom emoji fallback when media access is revoked', async () => {
    vi.mocked(groupApi.media).mockRejectedValue(new Error('Forbidden'))
    const wrapper = setup({ formatted_body: '<p>hello <img alt=":wave:" src="mxc://private/emoji"></p>', custom_emojis: [{ shortcode: 'wave', url: emojiURL, static_url: emojiURL }] })
    await flushPromises(); expect(wrapper.find('.group-message__body img').exists()).toBe(false); expect(wrapper.text()).toContain(':wave:'); wrapper.unmount()
  })
  it('renders audio/video as downloadable files without any player', async () => {
    const wrapper = setup({ kind: 'file', attachments: [{ id: 'a', name: 'video.mp4', type: 'video/mp4', size: 9, url: '/api/v1/chat/groups/g/messages/m/media/a' }] })
    expect(wrapper.find('video').exists()).toBe(false); expect(wrapper.find('audio').exists()).toBe(false); expect(wrapper.text()).toContain('下载'); expect(groupApi.media).not.toHaveBeenCalled(); wrapper.unmount()
  })
  const image = { id: 'image', name: 'picture.png', type: 'image/png', size: 9, url: '/api/v1/chat/groups/g/messages/m/media/image' }
  it('automatically displays ordinary images through authenticated media', async () => {
    const wrapper = setup({ kind: 'image', attachments: [image] })
    await flushPromises()
    expect(groupApi.media).toHaveBeenCalledWith(image.url, expect.any(AbortSignal))
    expect(wrapper.get('.group-attachment img').attributes('src')).toBe('blob:trusted-image')
    wrapper.unmount()
  })
  it('hides all group images for an NSFW topic, without requesting them before reveal', async () => {
    const wrapper = mount(GroupMessageView, { props: { message: { ...message, attachments: [image] }, groupTopic: '群介绍：#NSFW', canSend: true, canRedact: false }, global: { stubs: { GroupAvatar: true } } })
    await flushPromises()
    expect(groupApi.media).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('敏感图片 · 点击查看')
    expect(wrapper.html()).not.toContain(image.url)
    await wrapper.get('.group-attachment__preview').trigger('click'); await flushPromises()
    expect(wrapper.find('.group-attachment img').exists()).toBe(true)
    wrapper.unmount()
  })
  it('message NSFW tags hide only that message, including formatted text tags', async () => {
    const hidden = setup({ body: 'photo #nsfw', attachments: [image] })
    const formatted = setup({ formatted_body: '<p>photo <strong>#NSFW</strong></p>', attachments: [image] })
    const plain = setup({ id: 'plain', body: 'ordinary photo', attachments: [{ ...image, url: image.url + '2' }] })
    await flushPromises()
    expect(groupApi.media).toHaveBeenCalledTimes(1)
    expect(hidden.find('.group-attachment img').exists()).toBe(false)
    expect(formatted.find('.group-attachment img').exists()).toBe(false)
    expect(plain.find('.group-attachment img').exists()).toBe(true)
    hidden.unmount(); formatted.unmount(); plain.unmount()
  })
  it('honors explicit always-show and always-hide media preferences', async () => {
    const wrapper = setup({ body: '#NSFW', attachments: [image] })
    await flushPromises(); expect(groupApi.media).not.toHaveBeenCalled()
    await useMediaMode().setMode('show_all'); await flushPromises()
    expect(wrapper.find('.group-attachment img').exists()).toBe(true)
    await useMediaMode().setMode('hide_all'); await flushPromises()
    expect(wrapper.find('.group-attachment img').exists()).toBe(false)
    await wrapper.setProps({ message: { ...message, attachments: [image] } }); await flushPromises()
    expect(wrapper.find('.group-attachment img').exists()).toBe(false)
    expect(groupApi.media).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
  it('resets a manual reveal when the viewer explicitly switches to always-hide', async () => {
    const wrapper = setup({ body: '#NSFW', attachments: [image] })
    await wrapper.get('.group-attachment__preview').trigger('click'); await flushPromises()
    expect(wrapper.find('.group-attachment img').exists()).toBe(true)
    await useMediaMode().setMode('hide_all'); await flushPromises()
    expect(wrapper.find('.group-attachment img').exists()).toBe(false)
    expect(groupApi.media).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
  it('retries a failed download as a download, not an inert preview', async () => {
    vi.mocked(groupApi.media).mockRejectedValueOnce(new Error('Temporary failure')).mockResolvedValueOnce(new Blob(['file'], { type: 'text/plain' }))
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    const wrapper = mount(GroupAttachment, { props: { attachment: { id: 'f', name: 'notes.txt', type: 'text/plain', size: 4, url: '/api/v1/chat/groups/g/messages/m/media/f' } } })
    await wrapper.findAll('button').find(b => b.text() === '下载')!.trigger('click'); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === '重试')!.trigger('click'); await flushPromises()
    expect(click).toHaveBeenCalledTimes(1); wrapper.unmount(); click.mockRestore()
  })
})
