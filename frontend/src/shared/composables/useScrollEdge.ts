import { onMounted, onUnmounted } from 'vue'

/**
 * Scroll-edge effect: floating bars only show their edge once content
 * actually passes underneath them.
 *
 * `data-scrolled` — the top bar (content has left the top).
 * `data-scroll-more` — the bottom tab bar (more content still sits below).
 *
 * One passive listener, rAF-throttled. A resize observer catches layout
 * changes that don't fire scroll (images, new posts).
 */
export function useScrollEdge(threshold = 2) {
  let frame = 0
  let observer: ResizeObserver | null = null
  const update = () => {
    frame = 0
    const root = document.documentElement
    const scrolled = window.scrollY > threshold
    const max = Math.max(0, root.scrollHeight - window.innerHeight)
    const more = max - window.scrollY > threshold
    if (scrolled !== root.hasAttribute('data-scrolled')) {
      root.toggleAttribute('data-scrolled', scrolled)
    }
    if (more !== root.hasAttribute('data-scroll-more')) {
      root.toggleAttribute('data-scroll-more', more)
    }
  }
  const onScroll = () => {
    if (!frame) frame = requestAnimationFrame(update)
  }
  onMounted(() => {
    update()
    window.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', onScroll, { passive: true })
    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(onScroll)
      observer.observe(document.documentElement)
    }
  })
  onUnmounted(() => {
    window.removeEventListener('scroll', onScroll)
    window.removeEventListener('resize', onScroll)
    observer?.disconnect()
    if (frame) cancelAnimationFrame(frame)
    document.documentElement.removeAttribute('data-scrolled')
    document.documentElement.removeAttribute('data-scroll-more')
  })
}
