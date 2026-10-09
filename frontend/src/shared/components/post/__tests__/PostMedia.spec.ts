import { describe, expect, it, vi, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { mount } from '@vue/test-utils'
import PostMedia from '../PostMedia.vue'
import type { MastodonAttachment } from '@/shared/types/mastodon'
const file = (updates: Partial<MastodonAttachment> = {}): MastodonAttachment => ({ id: '1', type: 'unknown', url: 'https://remote.test/report.txt', preview_url: null, remote_url: null, text_url: null, meta: null, description: null, blurhash: null, ...updates })
describe('original attachment access', () => {
  it('exposes unknown files as real original links without triggering the enclosing post', async () => {
    const wrapper = mount(PostMedia, { props: { attachments: [file({ description: '<script>file</script>' })] } })
    const link = wrapper.get('a')
    expect(link.attributes('href')).toBe('https://remote.test/report.txt')
    expect(link.attributes('rel')).toBe('noopener noreferrer')
    expect(wrapper.find('script').exists()).toBe(false)
    expect(link.text()).toContain('<script>file</script>')
    let bubbled = false
    wrapper.element.addEventListener('click', () => { bubbled = true })
    await link.trigger('click')
    expect(bubbled).toBe(false)
  })
  it('keeps every original accessible beyond the four-image preview limit', () => {
    const attachments = Array.from({ length: 5 }, (_, i) => file({ id: String(i), type: 'image', url: `https://remote.test/full${i}.png`, preview_url: `https://remote.test/thumb${i}.png` }))
    const wrapper = mount(PostMedia, { props: { attachments } })
    expect(wrapper.findAll('img')).toHaveLength(4)
    // Previewed images are not listed again; only the fifth gets a link.
    expect(wrapper.findAll('a')).toHaveLength(1)
    expect(wrapper.get('a').attributes('href')).toBe('https://remote.test/full4.png')
  })
  it('loads uploaded files from the current site instead of the configured host', () => {
    const wrapper = mount(PostMedia, { props: { attachments: [file({ type: 'image', url: 'http://127.0.0.1:8080/media/9/photo.png', preview_url: 'http://127.0.0.1:8080/media/9/thumbnail/photo.jpg' })] } })
    expect(wrapper.get('img').attributes('src')).toBe('/media/9/thumbnail/photo.jpg')
    const doc = mount(PostMedia, { props: { attachments: [file({ url: 'http://127.0.0.1:8080/media/9/doc.pdf' })] } })
    expect(doc.get('a').attributes('href')).toBe('/media/9/doc.pdf')
  })
  it('shows images, videos and audio once, without a duplicate link list', async () => {
    const wrapper = mount(PostMedia, { props: { attachments: [
      file({ id: 'i', type: 'image', url: 'https://remote.test/a.png' }),
      file({ id: 'v', type: 'video', url: 'https://remote.test/v.mp4' }),
      file({ id: 'a', type: 'audio', url: 'https://remote.test/a.mp3' }),
    ] } })
    expect(wrapper.find('.media-file-links').exists()).toBe(false)
    expect(wrapper.find('video').exists()).toBe(true)
    // A broken preview falls back to the original link.
    await wrapper.get('img').trigger('error')
    expect(wrapper.get('.media-file-links a').attributes('href')).toBe('https://remote.test/a.png')
  })
  it('never creates executable, data or credential-bearing file links', () => {
    const wrapper = mount(PostMedia, { props: { attachments: ['javascript:alert(1)', 'data:text/html,hello', 'https://name:password@remote.test/file', ''].map((url, i) => file({ id: String(i), url })) } })
    expect(wrapper.find('a').exists()).toBe(false)
  })
  it('loads proxied media from this site and, only if that fails too, links the original', async () => {
    const proxied = 'https://other.host/proxy/media/TOKEN1'
    const wrapper = mount(PostMedia, { props: { attachments: [file({ type: 'image', url: proxied, preview_url: 'https://other.host/proxy/media/TOKEN2', remote_url: 'https://wxw.moe/a.png' })] } })
    expect(wrapper.get('img').attributes('src')).toBe('/proxy/media/TOKEN2')
    expect(wrapper.find('.media-file-links').exists()).toBe(false)
    await wrapper.get('img').trigger('error')
    expect(wrapper.findAll('.media-file-links a')).toHaveLength(1)
    expect(wrapper.get('.media-file-links a').attributes('href')).toBe('https://wxw.moe/a.png')
  })
  describe('expired proxy links', () => {
    afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })
    const stale = file({ id: 'm', type: 'image', url: 'https://c.test/proxy/media/OLD', preview_url: 'https://c.test/proxy/media/OLDP', remote_url: 'https://wxw.moe/a.png' })
    const fresh = file({ id: 'm', type: 'image', url: 'https://c.test/proxy/media/NEW', preview_url: 'https://c.test/proxy/media/NEWP', remote_url: 'https://wxw.moe/a.png' })

    it('asks for the status once on 410 and shows the renewed link', async () => {
      const fetchMock = vi.fn().mockResolvedValue({ status: 410 })
      vi.stubGlobal('fetch', fetchMock)
      const get = vi.spyOn(postApi, 'getStatus').mockResolvedValue({ id: 's1', media_attachments: [fresh] } as never)
      const wrapper = mount(PostMedia, { props: { attachments: [stale], statusId: 's1' } })
      await wrapper.get('img').trigger('error')
      await flushPromises()
      expect(fetchMock).toHaveBeenCalledWith('/proxy/media/OLDP', { method: 'HEAD' })
      expect(get).toHaveBeenCalledTimes(1)
      expect(wrapper.get('img').attributes('src')).toBe('/proxy/media/NEWP')
      expect(wrapper.find('.media-file-links').exists()).toBe(false)
      // A second failure does not loop: the original link is offered instead.
      await wrapper.get('img').trigger('error')
      await flushPromises()
      expect(get).toHaveBeenCalledTimes(1)
      expect(wrapper.get('.media-file-links a').attributes('href')).toBe('https://wxw.moe/a.png')
    })

    it('treats 403 as expired too, but not a plain failure such as 502', async () => {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ status: 502 }))
      const get = vi.spyOn(postApi, 'getStatus')
      const wrapper = mount(PostMedia, { props: { attachments: [stale], statusId: 's1' } })
      await wrapper.get('img').trigger('error')
      await flushPromises()
      expect(get).not.toHaveBeenCalled()
      expect(wrapper.get('.media-file-links a').attributes('href')).toBe('https://wxw.moe/a.png')

      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ status: 403 }))
      vi.spyOn(postApi, 'getStatus').mockResolvedValue({ id: 's1', media_attachments: [fresh] } as never)
      const w2 = mount(PostMedia, { props: { attachments: [stale], statusId: 's1' } })
      await w2.get('img').trigger('error')
      await flushPromises()
      expect(w2.get('img').attributes('src')).toBe('/proxy/media/NEWP')
    })

    it('falls back gracefully when the status cannot be loaded', async () => {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ status: 410 }))
      vi.spyOn(postApi, 'getStatus').mockRejectedValue(new Error('offline'))
      const wrapper = mount(PostMedia, { props: { attachments: [stale], statusId: 's1' } })
      await wrapper.get('img').trigger('error')
      await flushPromises()
      expect(wrapper.find('img').exists()).toBe(false)
      expect(wrapper.get('.media-file-links a').attributes('href')).toBe('https://wxw.moe/a.png')
    })
  })
})
