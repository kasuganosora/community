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
 * its last message clear of it. Writes are rAF-batched and skipped when the
 * rounded height is unchanged; the observed element is position: fixed, so
 * the padding the variable feeds cannot resize it (no observer loop).
 */
export function useDockedComposer(element: Ref<HTMLElement | null | undefined>): void {
  let observer: ResizeObserver | undefined
  let frame = 0, pending = -1, written = -1
  const root = () => document.documentElement
  const flush = () => {
    frame = 0
    if (pending === written) return
    written = pending
    root().style.setProperty('--composer-h', `${written}px`)
  }
  const publish = (height: number) => {
    pending = Math.ceil(height)
    if (pending !== written && !frame) frame = requestAnimationFrame(flush)
  }
  const attach = (el: HTMLElement | null | undefined) => {
    observer?.disconnect()
    if (!el) { if (frame) cancelAnimationFrame(frame); frame = 0; written = pending = -1; root().style.removeProperty('--composer-h'); return }
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
  onBeforeUnmount(() => { observer?.disconnect(); if (frame) cancelAnimationFrame(frame); root().style.removeProperty('--composer-h') })
}

/** How much shorter the visual viewport must be before we treat it as a keyboard. */
export const KEYBOARD_THRESHOLD = 120

function editableFocused(): boolean {
  const el = document.activeElement
  return el instanceof HTMLElement && el.matches('input:not([type=checkbox]):not([type=radio]):not([type=button]):not([type=submit]), textarea, select, [contenteditable=""], [contenteditable="true"]')
}

/**
 * Keyboard handling for the docked bars. Outside of typing nothing is
 * written: the tab bar and composer are plain CSS position: fixed; bottom.
 * Only while an editable element is focused AND visualViewport is more than
 * KEYBOARD_THRESHOLD px shorter than innerHeight (iOS overlays the keyboard)
 * does <html> get .keyboard-open and --keyboard-inset. Viewport scroll is
 * only listened to while that state is active, and writes are rAF-batched
 * and skipped when unchanged. Android with interactive-widget=resizes-content
 * shrinks innerHeight together with the visual viewport, so no inset is ever
 * needed there and nothing is written.
 */
export function useKeyboardInset(): void {
  if (typeof window === 'undefined' || !window.visualViewport) return
  const viewport = window.visualViewport
  let frame = 0, open = false, inset = -1
  const write = () => {
    frame = 0
    const html = document.documentElement
    const covered = Math.round(window.innerHeight - viewport.height - viewport.offsetTop)
    const next = editableFocused() && window.innerHeight - viewport.height > KEYBOARD_THRESHOLD
    if (next !== open) {
      open = next
      html.classList.toggle('keyboard-open', open)
      if (open) viewport.addEventListener('scroll', schedule, { passive: true })
      else { viewport.removeEventListener('scroll', schedule); html.style.removeProperty('--keyboard-inset'); inset = -1 }
    }
    if (open) {
      const value = Math.max(0, covered)
      if (value !== inset) { inset = value; html.style.setProperty('--keyboard-inset', `${value}px`) }
    }
  }
  const schedule = () => { if (!frame) frame = requestAnimationFrame(write) }
  // Resize without a focused field (toolbar collapse, rotation) is ignored
  // unless the keyboard state is currently active and needs clearing.
  const onResize = () => { if (open || editableFocused()) schedule() }
  onMounted(() => {
    viewport.addEventListener('resize', onResize, { passive: true })
    window.addEventListener('focusin', schedule)
    window.addEventListener('focusout', schedule)
  })
  onBeforeUnmount(() => {
    viewport.removeEventListener('resize', onResize)
    viewport.removeEventListener('scroll', schedule)
    window.removeEventListener('focusin', schedule)
    window.removeEventListener('focusout', schedule)
    if (frame) cancelAnimationFrame(frame)
    document.documentElement.classList.remove('keyboard-open')
    document.documentElement.style.removeProperty('--keyboard-inset')
  })
}
