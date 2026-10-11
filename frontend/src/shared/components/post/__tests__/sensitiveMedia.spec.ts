import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PostContent from '../PostContent.vue'
import PostMedia from '../PostMedia.vue'
import BlurPlaceholder from '../BlurPlaceholder.vue'
import ProfileMediaGrid from '@/domains/user/presentation/components/ProfileMediaGrid.vue'
import PreviewStatus from '@/domains/auth/presentation/components/PreviewStatus.vue'
import { decodeBlurhash, isValidBlurhash } from '@/shared/utils/blurhash'
import { mediaGated, useMediaMode } from '@/shared/composables/useMediaMode'
import type { MastodonAttachment, MastodonStatus } from '@/shared/types/mastodon'

vi.mock('@/shared/api/client', () => ({ default: { get: vi.fn().mockResolvedValue({ data: { 'reading:expand:media': 'hide_all' } }), patch: vi.fn().mockResolvedValue({ data: {} }) } }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn().mockResolvedValue(undefined) }) }))

const HASH = 'LEHV6nWB2yk8pyo0adR*.7kCMdnj'
const REAL = 'https://home.test/proxy/media/REALTOKEN'
const att = (over: Partial<MastodonAttachment> = {}): MastodonAttachment => ({
  id: '1', type: 'image', url: REAL, preview_url: `${REAL}-small`, remote_url: 'https://origin.test/secret.png?sensitive=true',
  text_url: null, meta: { original: { width: 400, height: 200, aspect: 2 } }, description: 'alt', blurhash: HASH, ...over,
})
const status = (over: Partial<MastodonStatus> = {}) => ({
  id: '9', content: '<p>x</p>', spoiler_text: '', sensitive: true, media_attachments: [att()], card: null, emojis: [], mentions: [], tags: [],
  account: { acct: 'a', username: 'a', emojis: [] }, ...over,
}) as unknown as MastodonStatus

function realUrlsIn(html: string) {
  return /REALTOKEN|secret\.png/.test(html)
}

describe('blurhash decoder', () => {
  it('validates and decodes', () => {
    expect(isValidBlurhash(HASH)).toBe(true)
    expect(isValidBlurhash('short')).toBe(false)
    expect(isValidBlurhash('LEHV6nWB2yk8pyo0adR*.7kCMdn')).toBe(false)
    const px = decodeBlurhash(HASH, 8, 6)
    expect(px).toHaveLength(8 * 6 * 4)
    expect(px[3]).toBe(255)
    expect(() => decodeBlurhash('nope', 4, 4)).toThrow()
  })
})

describe('BlurPlaceholder', () => {
  const ctx = { createImageData: (w: number, h: number) => ({ data: new Uint8ClampedArray(w * h * 4) }), putImageData: vi.fn() }
  beforeEach(() => { vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(ctx as never) })
  afterEach(() => { vi.restoreAllMocks(); ctx.putImageData.mockClear() })

  it('paints the blurhash on a canvas without any network element', () => {
    const w = mount(BlurPlaceholder, { props: { blurhash: HASH, aspect: 2 } })
    expect(w.find('canvas').exists()).toBe(true)
    expect(w.attributes('data-blurhash')).toBe(HASH)
    expect(w.attributes('style')).toContain('aspect-ratio: 2')
    expect(ctx.putImageData).toHaveBeenCalledTimes(1)
    expect(w.find('img, video, audio, source').exists()).toBe(false)
  })
  it('falls back to a plain gradient without a hash', () => {
    const w = mount(BlurPlaceholder, { props: { blurhash: '' } })
    expect(w.find('canvas').exists()).toBe(false)
    expect(w.classes()).toContain('blur-ph--plain')
  })
})

describe('hidden media never carries the real URL', () => {
  beforeEach(() => { vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null) })
  afterEach(() => vi.restoreAllMocks())

  it('PostMedia: images, video and audio render placeholders only', () => {
    const w = mount(PostMedia, { props: { hidden: true, attachments: [att(), att({ id: '2', type: 'video' }), att({ id: '3', type: 'audio', blurhash: null }), att({ id: '9', type: 'unknown' })] } })
    expect(w.findAll('img, video, audio, source, a')).toHaveLength(0)
    expect(w.findAll('.blur-ph').length).toBeGreaterThanOrEqual(3)
    expect(realUrlsIn(w.html())).toBe(false)
    expect(w.html()).not.toMatch(/srcset|poster/)
  })
  it('PostMedia: real files appear when not hidden', () => {
    const w = mount(PostMedia, { props: { hidden: false, attachments: [att()] } })
    expect(w.find('img').attributes('src')).toContain('REALTOKEN')
  })

  it('PostContent: no real URL until click, then reveal and hide again', async () => {
    const w = mount(PostContent, { props: { status: status() } })
    expect(realUrlsIn(w.html())).toBe(false)
    expect(w.find('.blur-ph').exists()).toBe(true)
    await w.get('.post-content__sensitive').trigger('click')
    expect(w.find('img').attributes('src')).toContain('REALTOKEN')
    expect(w.find('.post-content__sensitive').exists()).toBe(false)
    await w.get('.post-content__rehide').trigger('click')
    expect(realUrlsIn(w.html())).toBe(false)
    expect(w.find('.post-content__sensitive').exists()).toBe(true)
  })
  it('PostContent: an unflagged status shows its image directly', () => {
    const w = mount(PostContent, { props: { status: status({ sensitive: false }) } })
    expect(w.find('img').exists()).toBe(true)
    expect(w.find('.post-content__sensitive').exists()).toBe(false)
  })
  it('PostContent: link-card image of a gated status is withheld', async () => {
    const card = { url: 'https://news.test/a', title: 'T', description: '', type: 'link', image: 'https://home.test/card-images/CARDIMG.png', provider_name: 'N', provider_url: '' }
    const w = mount(PostContent, { props: { status: status({ media_attachments: [], card } as never) } })
    expect(w.html()).not.toContain('CARDIMG')
    expect(w.find('a.link-preview').exists()).toBe(true)
  })

  it('ProfileMediaGrid: hidden tiles have no img/video', async () => {
    const w = mount(ProfileMediaGrid, { props: { statuses: [status({ media_attachments: [att(), att({ id: '2', type: 'video' })] })] }, global: { stubs: { 't-icon': true, 't-loading': true } } })
    expect(w.findAll('img, video')).toHaveLength(0)
    expect(realUrlsIn(w.html())).toBe(false)
    await w.get('button').trigger('click')
    expect(w.find('img').exists()).toBe(true)
  })
  it('PreviewStatus (guests): placeholder until click', async () => {
    const w = mount(PreviewStatus, { props: { status: status() }, global: { stubs: { 't-icon': true, RouterLink: true } } })
    expect(realUrlsIn(w.html())).toBe(false)
    await w.get('.preview-status__reveal').trigger('click')
    expect(w.find('img').exists()).toBe(true)
  })
})

describe('media display preference', () => {
  it('gates according to the mode', () => {
    expect(mediaGated({ sensitive: true }, 'default')).toBe(true)
    expect(mediaGated({ sensitive: false }, 'default')).toBe(false)
    expect(mediaGated({ sensitive: true }, 'show_all')).toBe(false)
    expect(mediaGated({ sensitive: false }, 'hide_all')).toBe(true)
  })
  it('adopts the saved account mode and drives PostContent', async () => {
    const { loadMode, mode, setMode } = useMediaMode()
    await loadMode()
    expect(mode.value).toBe('hide_all')
    const w = mount(PostContent, { props: { status: status({ sensitive: false }) } })
    expect(w.find('.post-content__sensitive').exists()).toBe(true)
    expect(w.find('img').exists()).toBe(false)
    await setMode('show_all')
    await w.vm.$nextTick()
    expect(w.find('img').exists()).toBe(true)
    expect(localStorage.getItem('media-mode')).toBe('show_all')
    await setMode('default')
  })
})
