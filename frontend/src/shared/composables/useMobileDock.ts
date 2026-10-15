import { onBeforeUnmount, onMounted, watch, type Ref } from 'vue'

/** Same breakpoint as the phone layout in AppLayout (43.74rem ≈ 699px). */
export const MOBILE_QUERY = '(max-width: 43.74rem)'

export function isMobileLayout(): boolean {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia(MOBILE_QUERY).matches
}

/** Scroll the document (the phone scroller) to its end. */
export function scrollDocumentToEnd(): void {
  if (typeof window === 'undefined') return
  const root = document.scrollingElement ?? document.documentElement
  window.scrollTo({ top: root.scrollHeight, behavior: 'auto' })
}

/** True when the document is scrolled to within `slack` px of its end. */
export function documentNearEnd(slack = 80): boolean {
  const root = document.scrollingElement ?? document.documentElement
  return window.scrollY + window.innerHeight >= root.scrollHeight - slack
}

/**
 * Publishes the height of a composer that is docked (position: fixed) above
 * the phone tab bar as --composer-h on <html>, so the message list can keep
 * its last message clear of it.
 */
export function useDockedComposer(element: Ref<HTMLElement | null | undefined>): void {
  let observer: ResizeObserver | undefined
  const root = () => document.documentElement
  const publish = (height: number) => root().style.setProperty('--composer-h', `${Math.ceil(height)}px`)
  const attach = (el: HTMLElement | null | undefined) => {
    observer?.disconnect()
    if (!el) { root().style.removeProperty('--composer-h'); return }
    publish(el.getBoundingClientRect().height)
    if (typeof ResizeObserver === 'undefined') return
    observer = new ResizeObserver(entries => {
      const entry = entries[0]
      if (!entry) return
      const box = entry.borderBoxSize?.[0]
      publish(box ? box.blockSize : (entry.target as HTMLElement).getBoundingClientRect().height)
    })
    observer.observe(el)
  }
  onMounted(() => attach(element.value))
  watch(element, el => attach(el))
  onBeforeUnmount(() => { observer?.disconnect(); root().style.removeProperty('--composer-h') })
}

/**
 * Tracks the on-screen keyboard through visualViewport. While it is open
 * <html> gets .keyboard-open (the tab bar steps aside) and --keyboard-inset is
 * the part of the layout viewport the keyboard covers (iOS overlays the
 * keyboard; Android with interactive-widget=resizes-content shrinks the
 * viewport instead, where the inset stays 0).
 */
export function useKeyboardInset(): void {
  if (typeof window === 'undefined' || !window.visualViewport) return
  const viewport = window.visualViewport
  let tallest = viewport.height
  const update = () => {
    const html = document.documentElement
    tallest = Math.max(tallest, viewport.height, window.innerHeight)
    const typing = document.activeElement instanceof HTMLElement && document.activeElement.matches('input, textarea, [contenteditable="true"]')
    const open = typing && viewport.height < tallest - 120
    const inset = Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop)
    html.classList.toggle('keyboard-open', open)
    html.style.setProperty('--keyboard-inset', `${open ? Math.round(inset) : 0}px`)
  }
  const reset = () => { tallest = viewport.height; update() }
  onMounted(() => {
    viewport.addEventListener('resize', update)
    viewport.addEventListener('scroll', update)
    window.addEventListener('focusin', update)
    window.addEventListener('focusout', update)
    window.addEventListener('orientationchange', reset)
    update()
  })
  onBeforeUnmount(() => {
    viewport.removeEventListener('resize', update)
    viewport.removeEventListener('scroll', update)
    window.removeEventListener('focusin', update)
    window.removeEventListener('focusout', update)
    window.removeEventListener('orientationchange', reset)
    document.documentElement.classList.remove('keyboard-open')
  })
}
