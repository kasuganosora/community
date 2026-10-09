import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PostContent from '../PostContent.vue'
import type { MastodonCard, MastodonStatus } from '@/shared/types/mastodon'

const card = (updates: Partial<MastodonCard> = {}): MastodonCard => ({
  url: 'https://news.test/a', title: 'Headline', description: 'Summary', type: 'link',
  image: 'https://home.test/card-images/x.png', provider_name: 'News', provider_url: '', ...updates,
})
const status = (c: MastodonCard | null) => ({
  id: '1', content: '<p>see https://news.test/a</p>', spoiler_text: '', sensitive: false,
  media_attachments: [], card: c, emojis: [], mentions: [], tags: [],
}) as unknown as MastodonStatus

describe('link preview card', () => {
  it('renders a compact nofollow link with a lazy image and escaped text', () => {
    const wrapper = mount(PostContent, { props: { status: status(card({ title: '<img src=x onerror=alert(1)>' })) } })
    const link = wrapper.get('a.post-quote--link')
    expect(link.attributes('href')).toBe('https://news.test/a')
    expect(link.attributes('rel')).toBe('nofollow noopener noreferrer')
    expect(link.attributes('target')).toBe('_blank')
    const img = link.get('img')
    expect(img.attributes('loading')).toBe('lazy')
    expect(img.attributes('referrerpolicy')).toBe('no-referrer')
    expect(link.text()).toContain('<img src=x onerror=alert(1)>')
    expect(link.find('strong img').exists()).toBe(false)
  })
  it('refuses non-http card URLs and images', () => {
    const bad = mount(PostContent, { props: { status: status(card({ url: 'javascript:alert(1)' })) } })
    expect(bad.find('a.post-quote--link').exists()).toBe(false)
    const noImage = mount(PostContent, { props: { status: status(card({ image: 'javascript:alert(1)' })) } })
    expect(noImage.find('a.post-quote--link img').exists()).toBe(false)
  })
  it('renders nothing without a card', () => {
    expect(mount(PostContent, { props: { status: status(null) } }).find('a.post-quote--link').exists()).toBe(false)
  })
})
