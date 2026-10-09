import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PostMedia from '../PostMedia.vue'
import PreviewStatus from '@/domains/auth/presentation/components/PreviewStatus.vue'
import type { MastodonAttachment, MastodonStatus } from '@/shared/types/mastodon'

vi.mock('@/shared/api/client', () => ({ default: { get: vi.fn(), patch: vi.fn() } }))

const att = (over: Partial<MastodonAttachment> = {}): MastodonAttachment => ({
  id: '1', type: 'image', url: 'https://h.test/proxy/media/A', preview_url: 'https://h.test/proxy/media/B', remote_url: null, text_url: null,
  meta: { original: { width: 707, height: 1000, aspect: 0.707 } }, description: null, blurhash: 'LEHV6nWB2yk8pyo0adR*.7kCMdnj', ...over,
})
const boxOf = (w: ReturnType<typeof mount>) => w.find('[class^="media-grid--"]').attributes('style')

describe('single media box', () => {
  it('uses the attachment aspect for placeholder and revealed image alike', async () => {
    const hidden = mount(PostMedia, { props: { hidden: true, attachments: [att()] } })
    const shown = mount(PostMedia, { props: { hidden: false, attachments: [att()] } })
    expect(boxOf(hidden)).toContain('--media-aspect: 0.707')
    expect(boxOf(shown)).toBe(boxOf(hidden))
    // the placeholder itself carries no ratio of its own: it fills the box
    expect(hidden.find('.blur-ph').attributes('style') ?? '').not.toContain('aspect-ratio')
  })
  it('derives the aspect from width/height and falls back to 16:9', () => {
    const noAspect = att({ meta: { original: { width: 300, height: 200 } } })
    expect(boxOf(mount(PostMedia, { props: { hidden: true, attachments: [noAspect] } }))).toContain('1.5')
    const unknown = att({ meta: null })
    const w = mount(PostMedia, { props: { hidden: true, attachments: [unknown] } })
    expect(Number(/--media-aspect: ([\d.]+)/.exec(boxOf(w) ?? '')?.[1])).toBeCloseTo(16 / 9, 3)
  })
  it('multi-image grids carry no per-image box', () => {
    const w = mount(PostMedia, { props: { hidden: true, attachments: [att(), att({ id: '2' })] } })
    expect(boxOf(w)).toBeUndefined()
  })
  it('video wrap uses the same box hidden and revealed', () => {
    const v = att({ type: 'video', meta: { original: { width: 1080, height: 1920, aspect: 0.5625 } } })
    const a = mount(PostMedia, { props: { hidden: true, attachments: [v] } }).find('.media-video-wrap').attributes('style')
    const b = mount(PostMedia, { props: { hidden: false, attachments: [v] } }).find('.media-video-wrap').attributes('style')
    expect(a).toContain('0.5625')
    expect(b).toBe(a)
  })
})

describe('landing preview', () => {
  const status = { id: '5', content: '<p>x</p>', spoiler_text: '', sensitive: true, media_attachments: [att()], card: null, emojis: [], mentions: [], tags: [],
    account: { acct: 'a', username: 'a', emojis: [], display_name: 'a' }, created_at: '2026-01-01T00:00:00Z' } as unknown as MastodonStatus
  it('keeps one box for placeholder and revealed thumbnail', async () => {
    const w = mount(PreviewStatus, { props: { status }, global: { stubs: { 't-icon': true } } })
    const before = w.find('.preview-status__thumb').attributes('style')
    expect(before).toContain('--media-aspect: 0.707')
    await w.get('.preview-status__reveal').trigger('click')
    expect(w.find('.preview-status__thumb').attributes('style')).toBe(before)
    expect(w.find('img').exists()).toBe(true)
  })
  it('a thumbnail that fails to load leaves an empty box', async () => {
    const w = mount(PreviewStatus, { props: { status: { ...status, sensitive: false } as MastodonStatus }, global: { stubs: { 't-icon': true } } })
    await w.get('img').trigger('error')
    expect(w.find('img').exists()).toBe(false)
    expect(w.find('.preview-status__thumb').exists()).toBe(true)
  })
})
