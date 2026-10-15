import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import ProgressiveImage from '../ProgressiveImage.vue'

const HASH = 'LEHV6nWB2yk8pyo0adR*.7kCMdnj'

describe('ProgressiveImage', () => {
  afterEach(() => vi.restoreAllMocks())

  it('shows the BlurHash and a spinner while loading, with the image hidden', () => {
    const w = mount(ProgressiveImage, { props: { src: '/media/a.jpg', blurhash: HASH } })
    expect(w.attributes('data-state')).toBe('loading')
    expect(w.attributes('aria-busy')).toBe('true')
    expect(w.find('.blur-ph').attributes('data-blurhash')).toBe(HASH)
    expect(w.find('.pimg__spinner').exists()).toBe(true)
    expect(w.get('.pimg__img').attributes('loading')).toBe('lazy')
  })

  it('fades the image in once loaded and drops the spinner', async () => {
    const w = mount(ProgressiveImage, { props: { src: '/media/a.jpg', blurhash: HASH } })
    await w.get('.pimg__img').trigger('load')
    expect(w.attributes('data-state')).toBe('loaded')
    expect(w.classes()).toContain('pimg--loaded')
    expect(w.classes()).not.toContain('pimg--instant')
    expect(w.find('.pimg__spinner').exists()).toBe(false)
    expect(w.emitted('load')).toHaveLength(1)
  })

  it('shows a broken mark over the BlurHash on error instead of spinning forever', async () => {
    const w = mount(ProgressiveImage, { props: { src: '/media/a.jpg', blurhash: HASH } })
    await w.get('.pimg__img').trigger('error')
    expect(w.attributes('data-state')).toBe('error')
    expect(w.find('.pimg__spinner').exists()).toBe(false)
    expect(w.find('.pimg__error svg').exists()).toBe(true)
    expect(w.find('.blur-ph').exists()).toBe(true)
    expect(w.find('.pimg__retry').exists()).toBe(false)
    expect(w.emitted('error')).toHaveLength(1)
  })

  it('offers a retry that reloads the image when retryable', async () => {
    const w = mount(ProgressiveImage, { props: { src: '/media/a.jpg', retryable: true } })
    const first = w.get('.pimg__img').element
    await w.get('.pimg__img').trigger('error')
    await w.get('.pimg__retry').trigger('click')
    expect(w.attributes('data-state')).toBe('loading')
    expect(w.get('.pimg__img').element).not.toBe(first)
  })

  it('skips the overlay and the fade for an image already in cache', async () => {
    vi.spyOn(HTMLImageElement.prototype, 'complete', 'get').mockReturnValue(true)
    vi.spyOn(HTMLImageElement.prototype, 'naturalWidth', 'get').mockReturnValue(640)
    const w = mount(ProgressiveImage, { props: { src: '/media/cached.jpg', blurhash: HASH } })
    await w.vm.$nextTick()
    expect(w.attributes('data-state')).toBe('loaded')
    expect(w.classes()).toContain('pimg--instant')
    expect(w.find('.pimg__spinner').exists()).toBe(false)
    expect(w.find('.blur-ph').exists()).toBe(false)
  })

  it('uses an already-loaded low-res stand-in instead of the BlurHash when given', () => {
    const w = mount(ProgressiveImage, { props: { src: '/full.jpg', blurhash: HASH, placeholderSrc: '/thumb.jpg', lazy: false } })
    expect(w.get('.pimg__stand-in').attributes('src')).toBe('/thumb.jpg')
    expect(w.find('.blur-ph').exists()).toBe(false)
    expect(w.get('.pimg__img').attributes('loading')).toBeUndefined()
  })
})
