import { describe, expect, it } from 'vitest'
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
    expect(wrapper.findAll('a')).toHaveLength(5)
    expect(wrapper.findAll('a')[4]!.attributes('href')).toBe('https://remote.test/full4.png')
  })
  it('loads uploaded files from the current site instead of the configured host', () => {
    const wrapper = mount(PostMedia, { props: { attachments: [file({ type: 'image', url: 'http://127.0.0.1:8080/media/9/photo.png', preview_url: 'http://127.0.0.1:8080/media/9/thumbnail/photo.jpg' })] } })
    expect(wrapper.get('img').attributes('src')).toBe('/media/9/thumbnail/photo.jpg')
    expect(wrapper.get('a').attributes('href')).toBe('/media/9/photo.png')
  })
  it('never creates executable, data or credential-bearing file links', () => {
    const wrapper = mount(PostMedia, { props: { attachments: ['javascript:alert(1)', 'data:text/html,hello', 'https://name:password@remote.test/file', ''].map((url, i) => file({ id: String(i), url })) } })
    expect(wrapper.find('a').exists()).toBe(false)
  })
})
