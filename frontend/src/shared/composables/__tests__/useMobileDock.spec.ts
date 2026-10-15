import { describe, expect, it, vi, afterEach } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { useDockedComposer, scrollDocumentToEnd, documentNearEnd } from '../useMobileDock'

afterEach(() => { vi.unstubAllGlobals(); document.documentElement.style.removeProperty('--composer-h') })

describe('useMobileDock', () => {
  it('publishes the composer height as --composer-h and clears it on unmount', async () => {
    let callback: ResizeObserverCallback | undefined
    vi.stubGlobal('ResizeObserver', class { constructor(cb: ResizeObserverCallback) { callback = cb } observe() {} disconnect() {} })
    const Host = defineComponent({ setup() { const el = ref<HTMLElement>(); useDockedComposer(el); return () => h('form', { ref: el }) } })
    const wrapper = mount(Host)
    await nextTick()
    expect(document.documentElement.style.getPropertyValue('--composer-h')).toBe('0px')
    callback!([{ borderBoxSize: [{ blockSize: 96.4, inlineSize: 390 }], target: wrapper.element } as unknown as ResizeObserverEntry], {} as ResizeObserver)
    expect(document.documentElement.style.getPropertyValue('--composer-h')).toBe('97px')
    wrapper.unmount()
    expect(document.documentElement.style.getPropertyValue('--composer-h')).toBe('')
  })

  it('scrolls the window, not an inner element, to the end', () => {
    const scrollTo = vi.fn()
    vi.stubGlobal('scrollTo', scrollTo)
    Object.defineProperty(document.documentElement, 'scrollHeight', { configurable: true, value: 3000 })
    scrollDocumentToEnd()
    expect(scrollTo).toHaveBeenCalledWith({ top: 3000, behavior: 'auto' })
    expect(documentNearEnd(80)).toBe(false)
  })
})
