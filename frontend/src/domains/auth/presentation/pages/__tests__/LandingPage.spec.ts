import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import type { MastodonStatus } from '@/shared/types/mastodon'
import client from '@/shared/api/client'
import LandingPage from '../LandingPage.vue'

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn(() => Promise.resolve()) }),
  useRoute: () => ({ query: {} }),
}))

vi.mock('@/shared/api/client', () => ({ default: { get: vi.fn() } }))

function status(id: number, overrides: Partial<MastodonStatus> = {}): MastodonStatus {
  return {
    id: String(id),
    created_at: new Date().toISOString(),
    content: `<p>post ${id}</p>`,
    spoiler_text: '',
    sensitive: false,
    visibility: 'public',
    emojis: [],
    media_attachments: [],
    replies_count: 0,
    reblogs_count: 0,
    favourites_count: 0,
    poll: null,
    card: null,
    reblog: null,
    account: { id: '9', username: 'a', acct: 'a@remote.example', display_name: 'A', emojis: [], avatar: '' },
    ...overrides,
  } as unknown as MastodonStatus
}

const sensitiveMedia = {
  sensitive: true,
  media_attachments: [{ id: 'm', type: 'image', url: 'https://remote.example/a.jpg', preview_url: 'https://remote.example/a.jpg' }],
} as Partial<MastodonStatus>

let wrapper: VueWrapper | undefined
afterEach(() => { wrapper?.unmount(); wrapper = undefined })

async function render(timeline: MastodonStatus[]) {
  vi.mocked(client.get).mockImplementation(async (url: string) => {
    if (url === '/api/v1/instance') return { data: { title: 'Community', stats: { user_count: 4, status_count: 11 } } }
    return { data: timeline }
  })
  wrapper = mount(LandingPage, {
    global: { stubs: { TButton: { template: '<button><slot /></button>' }, TIcon: true, TAvatar: true, LogoIcon: true } },
  })
  await flushPromises()
  return wrapper
}

const ids = (w: VueWrapper, selector: string) => w.findAll(`${selector} .preview-status__text`).map(el => el.text().replace(/[\u200e\u200f\u2066-\u2069]/g, ''))

describe('LandingPage', () => {
  it('never shows the same post in both columns', async () => {
    const w = await render(Array.from({ length: 12 }, (_, i) => status(i + 1)))
    const left = ids(w, '.preview-post')
    const right = ids(w, '.feed-note')
    expect(left).toEqual(['post 1', 'post 2', 'post 3', 'post 4', 'post 5'])
    expect(right).toEqual(['post 6', 'post 7', 'post 8', 'post 9', 'post 10', 'post 11', 'post 12'])
    expect(left.filter(text => right.includes(text))).toEqual([])
  })

  it('leaves the right column out when the card already shows every post', async () => {
    const w = await render([status(1), status(2)])
    expect(ids(w, '.preview-post')).toHaveLength(2)
    expect(w.find('.feed-column').exists()).toBe(false)
  })

  it('blurs sensitive media in both columns until revealed', async () => {
    const timeline = Array.from({ length: 8 }, (_, i) => status(i + 1, i === 1 || i === 6 ? sensitiveMedia : {}))
    const w = await render(timeline)
    for (const column of ['.preview-post', '.feed-note']) {
      const media = w.findAll(`${column} .preview-status__media`)
      expect(media).toHaveLength(1)
      expect(media[0]!.classes()).toContain('is-blurred')
      expect(media[0]!.find('.preview-status__reveal').exists()).toBe(true)
    }
    await w.get('.feed-note .preview-status__reveal').trigger('click')
    expect(w.get('.feed-note .preview-status__media').classes()).not.toContain('is-blurred')
    expect(w.get('.preview-post .preview-status__media').classes()).toContain('is-blurred')
  })

  it('clamps long posts in both columns with an expand button', async () => {
    const timeline = Array.from({ length: 6 }, (_, i) => status(i + 1, { content: `<p>${'long text '.repeat(200)}</p>` }))
    // jsdom has no layout: pretend every text block is taller than its clamp.
    const scroll = vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(500)
    const client = vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(100)
    try {
      const w = await render(timeline)
      for (const column of ['.preview-post', '.feed-note']) {
        const post = w.get(column)
        expect(post.find('.preview-status__text.is-clamped').exists()).toBe(true)
        const more = post.get('.preview-status__more')
        expect(more.text()).toBe('展开全文')
        await more.trigger('click')
        expect(post.find('.preview-status__text.is-clamped').exists()).toBe(false)
        expect(more.text()).toBe('收起')
      }
    } finally {
      scroll.mockRestore()
      client.mockRestore()
    }
  })
})
