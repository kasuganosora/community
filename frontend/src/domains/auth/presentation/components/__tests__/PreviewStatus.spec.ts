import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import PreviewStatus from '../PreviewStatus.vue'
import type { MastodonStatus } from '@/shared/types/mastodon'

const emoji = (shortcode: string) => ({ shortcode, url: `https://remote.example/${shortcode}.png`, static_url: `https://remote.example/${shortcode}.png`, visible_in_picker: false })

function makeStatus(overrides: Partial<MastodonStatus> = {}): MastodonStatus {
  return {
    id: '1',
    created_at: new Date().toISOString(),
    content: '<p>hi :aru_023: and :0130: <a href="https://remote.example/tags/x" class="mention hashtag">#x</a></p>',
    spoiler_text: '',
    sensitive: false,
    visibility: 'public',
    emojis: [emoji('aru_023'), emoji('0130')],
    media_attachments: [],
    replies_count: 0,
    reblogs_count: 0,
    favourites_count: 0,
    poll: null,
    card: null,
    reblog: null,
    account: { id: '9', username: 'd', acct: 'd@malu.today', display_name: 'D :blobcat_strawhat:', emojis: [emoji('blobcat_strawhat')], avatar: '' },
    ...overrides,
  } as unknown as MastodonStatus
}

function render(status: MastodonStatus) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }] })
  return mount(PreviewStatus, { props: { status }, global: { plugins: [router], stubs: { 't-icon': true, 't-avatar': true } } })
}

describe('PreviewStatus', () => {
  it('shows the author and renders custom emoji (including numeric shortcodes) as images', () => {
    const wrapper = render(makeStatus())
    expect(wrapper.text()).toContain('@d@malu.today')
    const alts = wrapper.findAll('img.custom-emoji').map(img => img.attributes('alt'))
    expect(alts).toEqual(expect.arrayContaining([':aru_023:', ':0130:', ':blobcat_strawhat:']))
    expect(wrapper.text()).not.toContain(':aru_023:')
    expect(wrapper.find('a[href="https://remote.example/tags/x"]').exists()).toBe(true)
  })

  it('hides the body and media behind the content warning', async () => {
    const wrapper = render(makeStatus({
      spoiler_text: 'spoiler :aru_023:',
      sensitive: true,
      media_attachments: [{ id: 'm', type: 'image', url: 'https://remote.example/a.jpg', preview_url: 'https://remote.example/a.jpg' }] as MastodonStatus['media_attachments'],
    }))
    expect(wrapper.find('.preview-status__text').exists()).toBe(false)
    expect(wrapper.find('.preview-status__thumbs').exists()).toBe(false)
    expect(wrapper.text()).toContain('1 个媒体')
    await wrapper.get('.preview-status__toggle').trigger('click')
    expect(wrapper.find('.preview-status__text').exists()).toBe(true)
    // Sensitive media stays blurred until revealed.
    expect(wrapper.find('.preview-status__media.is-blurred').exists()).toBe(true)
    await wrapper.get('.preview-status__reveal').trigger('click')
    expect(wrapper.find('.preview-status__media.is-blurred').exists()).toBe(false)
  })

  it('blurs sensitive media without a content warning until revealed', async () => {
    const wrapper = render(makeStatus({
      sensitive: true,
      media_attachments: [{ id: 'm', type: 'image', url: 'https://remote.example/a.jpg', preview_url: 'https://remote.example/a.jpg' }] as MastodonStatus['media_attachments'],
    }))
    expect(wrapper.find('.preview-status__text').exists()).toBe(true)
    expect(wrapper.get('.preview-status__media').classes()).toContain('is-blurred')
    await wrapper.get('.preview-status__reveal').trigger('click')
    expect(wrapper.get('.preview-status__media').classes()).not.toContain('is-blurred')
  })

  it('shows the boosted post with its own author', () => {
    const inner = makeStatus({ id: '2', content: '<p>original</p>' })
    const wrapper = render(makeStatus({ content: '', reblog: inner, account: { id: '3', username: 'luna', acct: 'luna@maid.lat', display_name: '露娜', emojis: [] } as unknown as MastodonStatus['account'] }))
    expect(wrapper.text()).toContain('露娜')
    expect(wrapper.text()).toContain('转发了')
    expect(wrapper.text()).toContain('original')
  })

  it('never turns hostile shortcodes or markup into live HTML', () => {
    const wrapper = render(makeStatus({
      content: '<p>:x: <img src=x onerror=alert(1)><script>alert(2)</script></p>',
      emojis: [{ shortcode: 'x', url: 'javascript:alert(3)', static_url: 'javascript:alert(3)', visible_in_picker: false }],
      account: { id: '9', username: 'e', acct: 'e@x', display_name: '<b>:"onerror=:</b>', emojis: [] } as unknown as MastodonStatus['account'],
    }))
    expect(wrapper.find('script,[onerror],img[src^="javascript:"],b').exists()).toBe(false)
    expect(wrapper.text()).toContain('<b>:"onerror=:</b>')
  })
})
