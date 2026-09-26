import { onMounted, onUnmounted } from 'vue'

/**
 * Scroll-edge effect: floating bars only show their hairline once content
 * actually scrolls underneath them. Sets `data-scrolled` on <html>; CSS does
 * the rest. One passive listener, rAF-throttled, no layout reads beyond
 * scrollY.
 */
export function useScrollEdge(threshold = 2) {
  let frame = 0
  const update = () => {
    frame = 0
    const scrolled = window.scrollY > threshold
    const root = document.documentElement
    if (scrolled !== root.hasAttribute('data-scrolled')) {
      root.toggleAttribute('data-scrolled', scrolled)
    }
  }
  const onScroll = () => {
    if (!frame) frame = requestAnimationFrame(update)
  }
  onMounted(() => {
    update()
    window.addEventListener('scroll', onScroll, { passive: true })
  })
  onUnmounted(() => {
    window.removeEventListener('scroll', onScroll)
    if (frame) cancelAnimationFrame(frame)
    document.documentElement.removeAttribute('data-scrolled')
  })
}
