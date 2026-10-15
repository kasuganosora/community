import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { useDockedComposer, useKeyboardInset, scrollDocumentToEnd, documentNearEnd } from '../useMobileDock'

let frames: FrameRequestCallback[] = []
const flushFrames = () => { const run = frames; frames = []; run.forEach(cb => cb(0)) }
beforeEach(() => {
  frames = []
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { frames.push(cb); return frames.length })
  vi.stubGlobal('cancelAnimationFrame', () => {})
})
afterEach(() => {
  vi.unstubAllGlobals()
  const html = document.documentElement
  html.style.removeProperty('--composer-h'); html.style.removeProperty('--keyboard-inset'); html.classList.remove('keyboard-open')
})

describe('useDockedComposer', () => {
  it('publishes the composer height once per frame, only when it changes, and clears it on unmount', async () => {
    let callback: ResizeObserverCallback | undefined
    vi.stubGlobal('ResizeObserver', class { constructor(cb: ResizeObserverCallback) { callback = cb } observe() {} disconnect() {} })
    const Host = defineComponent({ setup() { const el = ref<HTMLElement>(); useDockedComposer(el); return () => h('form', { ref: el }) } })
    const wrapper = mount(Host)
    await nextTick(); flushFrames()
    const html = document.documentElement
    expect(html.style.getPropertyValue('--composer-h')).toBe('0px')
    const setProperty = vi.spyOn(html.style, 'setProperty')
    const entry = (size: number) => [{ borderBoxSize: [{ blockSize: size, inlineSize: 390 }], target: wrapper.element } as unknown as ResizeObserverEntry]
    callback!(entry(90), {} as ResizeObserver)
    callback!(entry(96.4), {} as ResizeObserver)
    expect(frames).toHaveLength(1)
    flushFrames()
    expect(html.style.getPropertyValue('--composer-h')).toBe('97px')
    callback!(entry(96.2), {} as ResizeObserver)
    flushFrames()
    expect(setProperty).toHaveBeenCalledTimes(1)
    wrapper.unmount()
    expect(html.style.getPropertyValue('--composer-h')).toBe('')
  })
})

describe('useKeyboardInset', () => {
  function setup(height: number) {
    const viewport = Object.assign(new EventTarget(), { height, offsetTop: 0 })
    vi.stubGlobal('visualViewport', viewport)
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 844 })
    const wrapper = mount(defineComponent({ setup() { useKeyboardInset(); return () => h('textarea') } }), { attachTo: document.body })
    return { viewport, wrapper }
  }

  it('writes nothing while not typing, even when the viewport resizes or scrolls', () => {
    const { viewport, wrapper } = setup(844)
    const setProperty = vi.spyOn(document.documentElement.style, 'setProperty')
    viewport.height = 700
    viewport.dispatchEvent(new Event('resize')); viewport.dispatchEvent(new Event('scroll')); window.dispatchEvent(new Event('scroll'))
    flushFrames()
    expect(frames).toHaveLength(0)
    expect(setProperty).not.toHaveBeenCalled()
    expect(document.documentElement.classList.contains('keyboard-open')).toBe(false)
    wrapper.unmount()
  })

  it('opens only when an input is focused and the viewport is clearly shorter, then clears on blur', async () => {
    const { viewport, wrapper } = setup(844)
    const textarea = wrapper.element as HTMLTextAreaElement
    viewport.height = 760 // focused, but within the threshold (toolbar collapse)
    textarea.focus(); flushFrames()
    expect(document.documentElement.classList.contains('keyboard-open')).toBe(false)
    viewport.height = 500
    viewport.dispatchEvent(new Event('resize')); flushFrames()
    expect(document.documentElement.classList.contains('keyboard-open')).toBe(true)
    expect(document.documentElement.style.getPropertyValue('--keyboard-inset')).toBe('344px')
    textarea.blur(); flushFrames()
    expect(document.documentElement.classList.contains('keyboard-open')).toBe(false)
    expect(document.documentElement.style.getPropertyValue('--keyboard-inset')).toBe('')
    wrapper.unmount()
  })
})

describe('document scrolling helpers', () => {
  it('scrolls the window, not an inner element, to the end', () => {
    const scrollTo = vi.fn()
    vi.stubGlobal('scrollTo', scrollTo)
    Object.defineProperty(document.documentElement, 'scrollHeight', { configurable: true, value: 3000 })
    scrollDocumentToEnd()
    expect(scrollTo).toHaveBeenCalledWith({ top: 3000, behavior: 'auto' })
    expect(documentNearEnd(80)).toBe(false)
  })
})
