import { getCurrentInstance, onBeforeUnmount, watch, type Ref } from 'vue'
import { useRouter } from 'vue-router'

type Root = HTMLElement | null | undefined
export interface DismissOptions {
  /** Called instead of setting open=false (e.g. to restore focus). */
  onClose?: (reason: DismissReason) => void
  /** Close when the page scrolls (scrolls inside a root are ignored). Default true. */
  scroll?: boolean
  /** Close when focus moves outside every root. Default true. */
  focus?: boolean
}
export type DismissReason = 'outside' | 'escape' | 'focus' | 'scroll' | 'route'

/**
 * Shared dismissal for popovers, menus and pickers: closes on outside
 * pointerdown (mouse and touch), Escape, focus leaving, page scroll and
 * route change. Listeners exist only while open.
 */
export function useDismiss(open: Ref<boolean>, roots: () => Root | Root[], options: DismissOptions = {}) {
  const inside = (node: EventTarget | null) => {
    if (!(node instanceof Node)) return false
    const list = ([] as Root[]).concat(roots())
    return list.some(root => !!root && root.contains(node))
  }
  const close = (reason: DismissReason) => {
    if (!open.value) return
    if (options.onClose) options.onClose(reason)
    else open.value = false
  }
  const onPointer = (e: Event) => { if (!inside(e.target)) close('outside') }
  // Capture phase: runs before an enclosing sheet/dialog's own Escape
  // handler, which sees defaultPrevented and stays open.
  const onKey = (e: KeyboardEvent) => {
    if (e.key !== 'Escape' || e.defaultPrevented) return
    e.preventDefault()
    close('escape')
  }
  const onFocus = (e: FocusEvent) => { if (e.target !== window && !inside(e.target)) close('focus') }
  const onScroll = (e: Event) => { if (!inside(e.target)) close('scroll') }
  let bound = false
  function bind() {
    if (bound) return
    bound = true
    document.addEventListener('pointerdown', onPointer, true)
    document.addEventListener('keydown', onKey, true)
    if (options.focus !== false) document.addEventListener('focusin', onFocus)
    if (options.scroll !== false) window.addEventListener('scroll', onScroll, { capture: true, passive: true })
  }
  function unbind() {
    if (!bound) return
    bound = false
    document.removeEventListener('pointerdown', onPointer, true)
    document.removeEventListener('keydown', onKey, true)
    document.removeEventListener('focusin', onFocus)
    window.removeEventListener('scroll', onScroll, true)
  }
  watch(open, value => { if (value) bind(); else unbind() }, { immediate: true })
  let stopRoute: (() => void) | undefined
  if (getCurrentInstance()) {
    let router: ReturnType<typeof useRouter> | undefined
    try { router = useRouter() } catch { router = undefined }
    stopRoute = router?.afterEach(() => close('route'))
  }
  onBeforeUnmount(() => { unbind(); stopRoute?.() })
  return { close }
}
