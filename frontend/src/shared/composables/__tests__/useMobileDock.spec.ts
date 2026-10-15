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
  const html = document.documentElement
  function setup(markup: string, { mobile = true, viewportHeight = 844 } = {}) {
    vi.stubGlobal('matchMedia', (query: string) => ({ matches: mobile, media: query, addEventListener() {}, removeEventListener() {} }))
    const viewport = Object.assign(new EventTarget(), { height: viewportHeight, offsetTop: 0 })
    vi.stubGlobal('visualViewport', viewport)
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 844 })
    const wrapper = mount(defineComponent({ setup() { useKeyboardInset(); return () => h('div', { innerHTML: markup }) } }), { attachTo: document.body })
    return { viewport, wrapper, el: (selector: string) => wrapper.element.querySelector(selector) as HTMLElement }
  }

  it('marks the keyboard open on focus alone (Android resizes the viewport, so no size diff)', () => {
    const { wrapper, el } = setup('<textarea class="reply"></textarea>')
    el('.reply').focus()
    expect(html.classList.contains('keyboard-open')).toBe(true)
    expect(html.style.getPropertyValue('--keyboard-inset')).toBe('')
    el('.reply').blur(); flushFrames()
    expect(html.classList.contains('keyboard-open')).toBe(false)
    wrapper.unmount()
  })

  it('counts text-entry fields only, not checkboxes or buttons', () => {
    const { wrapper, el } = setup('<input class="search" type="search"><input class="plain"><input class="box" type="checkbox"><button class="btn">x</button><div class="ce" contenteditable="true"></div>')
    for (const selector of ['.search', '.plain', '.ce']) {
      el(selector).focus(); flushFrames()
      expect(html.classList.contains('keyboard-open'), selector).toBe(true)
    }
    for (const selector of ['.box', '.btn']) {
      el(selector).focus(); flushFrames()
      expect(html.classList.contains('keyboard-open'), selector).toBe(false)
    }
    wrapper.unmount()
  })

  it('does not flicker when focus moves between fields', () => {
    const { wrapper, el } = setup('<input class="a" type="text"><textarea class="b"></textarea>')
    const toggles = vi.spyOn(html.classList, 'toggle')
    el('.a').focus(); el('.b').focus(); flushFrames()
    expect(html.classList.contains('keyboard-open')).toBe(true)
    expect(toggles).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('does nothing on desktop', () => {
    const { wrapper, el } = setup('<textarea class="reply"></textarea>', { mobile: false })
    el('.reply').focus(); flushFrames()
    expect(html.classList.contains('keyboard-open')).toBe(false)
    wrapper.unmount()
  })

  it('applies the iOS overlay inset only to chat composers', () => {
    const { viewport, wrapper, el } = setup('<textarea class="reply"></textarea><form class="chat-compose"><textarea class="chat"></textarea></form>', { viewportHeight: 500 })
    el('.reply').focus(); flushFrames()
    expect(html.style.getPropertyValue('--keyboard-inset')).toBe('')
    el('.chat').focus(); flushFrames()
    expect(html.style.getPropertyValue('--keyboard-inset')).toBe('344px')
    viewport.height = 450; viewport.dispatchEvent(new Event('resize')); flushFrames()
    expect(html.style.getPropertyValue('--keyboard-inset')).toBe('394px')
    el('.chat').blur(); flushFrames()
    expect(html.style.getPropertyValue('--keyboard-inset')).toBe('')
    expect(html.classList.contains('keyboard-open')).toBe(false)
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
