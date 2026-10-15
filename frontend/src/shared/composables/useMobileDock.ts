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

/** Elements that bring up the on-screen keyboard (not checkboxes, buttons, selects). */
export const TEXT_ENTRY_SELECTOR = [
  'textarea', '[contenteditable=""]', '[contenteditable="true"]', '[contenteditable="plaintext-only"]', 'input:not([type])',
  ...['text', 'search', 'email', 'url', 'password', 'number', 'tel'].map(type => `input[type="${type}" i]`),
].join(', ')

/** Chat composers that dock above the tab bar and follow the iOS keyboard. */
const CHAT_COMPOSER_SELECTOR = '.group-composer-dock, .chat-compose'

export function isTextEntry(el: Element | null | undefined): boolean {
  return el instanceof HTMLElement && el.matches(TEXT_ENTRY_SELECTOR) && !(el as HTMLInputElement).readOnly && !(el as HTMLInputElement).disabled
}

/**
 * Keyboard handling on phones.
 *
 * 1. Focus-based state (works on Android, where the layout viewport resizes
 *    and a visualViewport diff never shows up): while a text-entry element is
 *    focused <html> has .keyboard-open, which hides the tab bar and compose
 *    FAB and shrinks --tabbar-h to the safe area. focusout is re-checked on
 *    the next frame so moving between fields does not flicker.
 * 2. iOS overlay offset, chat composers only: while focus is inside a docked
 *    chat composer and visualViewport is more than KEYBOARD_THRESHOLD px
 *    shorter than innerHeight, --keyboard-inset lifts the composer onto the
 *    keyboard. Viewport listeners exist only during that time; writes are
 *    rAF-batched and skipped when unchanged.
 */
export function useKeyboardInset(): void {
  if (typeof window === 'undefined') return
  const viewport = window.visualViewport
  const html = () => document.documentElement
  let open = false, tracking = false, inset = -1, frame = 0, blurFrame = 0

  const setOpen = (next: boolean) => {
    if (next === open) return
    open = next
    html().classList.toggle('keyboard-open', open)
  }
  const writeInset = () => {
    frame = 0
    if (!viewport) return
    const covered = window.innerHeight - viewport.height > KEYBOARD_THRESHOLD
      ? Math.max(0, Math.round(window.innerHeight - viewport.height - viewport.offsetTop)) : 0
    if (covered === inset) return
    inset = covered
    if (covered) html().style.setProperty('--keyboard-inset', `${covered}px`)
    else html().style.removeProperty('--keyboard-inset')
  }
  const scheduleInset = () => { if (!frame) frame = requestAnimationFrame(writeInset) }
  const track = (next: boolean) => {
    if (!viewport || next === tracking) return
    tracking = next
    if (next) {
      viewport.addEventListener('resize', scheduleInset, { passive: true })
      viewport.addEventListener('scroll', scheduleInset, { passive: true })
      scheduleInset()
    } else {
      viewport.removeEventListener('resize', scheduleInset)
      viewport.removeEventListener('scroll', scheduleInset)
      if (frame) cancelAnimationFrame(frame)
      frame = 0; inset = -1
      html().style.removeProperty('--keyboard-inset')
    }
  }
  const sync = (el: Element | null) => {
    const typing = isMobileLayout() && isTextEntry(el)
    setOpen(typing)
    track(typing && !!el?.closest(CHAT_COMPOSER_SELECTOR))
  }
  const onFocusIn = (event: FocusEvent) => {
    if (blurFrame) { cancelAnimationFrame(blurFrame); blurFrame = 0 }
    sync(event.target as Element)
  }
  const onFocusOut = () => {
    if (blurFrame) return
    blurFrame = requestAnimationFrame(() => { blurFrame = 0; sync(document.activeElement) })
  }
  onMounted(() => {
    document.addEventListener('focusin', onFocusIn)
    document.addEventListener('focusout', onFocusOut)
    sync(document.activeElement)
  })
  onBeforeUnmount(() => {
    document.removeEventListener('focusin', onFocusIn)
    document.removeEventListener('focusout', onFocusOut)
    if (blurFrame) cancelAnimationFrame(blurFrame)
    track(false); setOpen(false)
  })
}
