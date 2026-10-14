import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import GroupAttachment from '../GroupAttachment.vue'
import { groupApi } from '../../../infrastructure/groupApi'

vi.mock('../../../infrastructure/groupApi', () => ({ groupApi: { media: vi.fn() } }))
const attachment = { id: 'image', name: 'picture.webp', type: 'image/webp', size: 9, url: '/api/v1/chat/groups/g/messages/m/media/image' }
const image = () => new Blob(['image'], { type: 'image/webp' })
const setup = (hidden = true) => mount(GroupAttachment, { props: { attachment, hidden, sensitive: hidden }, global: { stubs: { 't-icon': true } } })
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(groupApi.media).mockResolvedValue(image())
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:private-image'), revokeObjectURL: vi.fn() }))
})
afterEach(() => { vi.unstubAllGlobals() })

describe('group image visibility and attachment lifecycle', () => {
  it('loads an ordinary image only once it approaches the viewport', async () => {
    let intersection!: IntersectionObserverCallback
    const disconnect = vi.fn()
    vi.stubGlobal('IntersectionObserver', class {
      constructor(callback: IntersectionObserverCallback) { intersection = callback }
      observe = vi.fn()
      disconnect = disconnect
    })
    const wrapper = setup(false)
    await flushPromises(); expect(groupApi.media).not.toHaveBeenCalled()
    intersection([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver)
    await flushPromises()
    expect(groupApi.media).toHaveBeenCalledTimes(1)
    expect(wrapper.find('img').exists()).toBe(true)
    expect(disconnect).toHaveBeenCalled()
    wrapper.unmount()
  })
  it('requires separate consent for each attachment and resets it when its identity changes', async () => {
    const first = setup(), second = setup()
    await flushPromises(); expect(groupApi.media).not.toHaveBeenCalled()
    await first.get('.group-attachment__preview').trigger('click'); await flushPromises()
    expect(first.find('img').exists()).toBe(true)
    expect(second.find('img').exists()).toBe(false)
    await first.setProps({ attachment: { ...attachment, url: '/api/v1/chat/groups/other/messages/new/media/image' } }); await flushPromises()
    expect(first.find('img').exists()).toBe(false)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:private-image')
    expect(groupApi.media).toHaveBeenCalledTimes(1)
    first.unmount(); second.unmount()
  })
  it('re-hides a revealed image and releases its blob URL without refetching', async () => {
    const wrapper = setup()
    await wrapper.get('.group-attachment__preview').trigger('click'); await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '隐藏')!.trigger('click'); await flushPromises()
    expect(wrapper.find('img').exists()).toBe(false)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:private-image')
    expect(groupApi.media).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
  it('aborts an automatic load if the message becomes sensitive and ignores the stale response', async () => {
    let complete!: (blob: Blob) => void
    vi.mocked(groupApi.media).mockImplementationOnce(() => new Promise(resolve => { complete = resolve }))
    const wrapper = setup(false)
    await flushPromises()
    const signal = vi.mocked(groupApi.media).mock.calls[0]![1]!
    await wrapper.setProps({ hidden: true })
    expect(signal.aborted).toBe(true)
    complete(image()); await flushPromises()
    expect(URL.createObjectURL).not.toHaveBeenCalled()
    expect(wrapper.find('img').exists()).toBe(false)
    wrapper.unmount()
  })
  it('does not let an old room response replace a newer attachment', async () => {
    let complete!: (blob: Blob) => void
    vi.mocked(groupApi.media).mockImplementationOnce(() => new Promise(resolve => { complete = resolve }))
    const wrapper = setup(false)
    await flushPromises()
    await wrapper.setProps({ attachment: { ...attachment, url: '/api/v1/chat/groups/other/messages/new/media/image' } }); await flushPromises()
    expect(wrapper.find('img').exists()).toBe(true)
    complete(image()); await flushPromises()
    expect(URL.createObjectURL).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
  it('treats download as an explicit fetch without revealing a hidden image', async () => {
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    const wrapper = setup()
    await wrapper.findAll('button').find(button => button.text() === '下载')!.trigger('click'); await flushPromises()
    expect(click).toHaveBeenCalledTimes(1)
    expect(wrapper.find('img').exists()).toBe(false)
    await wrapper.get('.group-attachment__preview').trigger('click'); await flushPromises()
    expect(wrapper.find('img').exists()).toBe(true)
    expect(groupApi.media).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
  it('does not display active content when an image response has an unsafe MIME type', async () => {
    vi.mocked(groupApi.media).mockResolvedValueOnce(new Blob(['<script>bad()</script>'], { type: 'image/svg+xml' }))
    const wrapper = setup(false)
    await flushPromises()
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toContain('该附件无法预览')
    expect(vi.mocked(URL.createObjectURL).mock.calls[0]![0]).toHaveProperty('type', 'application/octet-stream')
    wrapper.unmount()
  })
})
