import { onBeforeUnmount, onMounted, readonly, ref, watch, type Ref } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { createSpring, prefersReducedMotion, rubberband, SPRING_DEFAULT } from '@/shared/motion/spring'

export const PULL_REFRESH_THRESHOLD = 48
const PULL_HOLD_OFFSET = 44
const PULL_RUBBER_DIMENSION = 160
const PULL_HYSTERESIS = 10

function isFormElement(target: EventTarget | null): boolean {
  return target instanceof Element
    && !!target.closest('input, textarea, select, [contenteditable="true"]')
}

function hasScrolledAncestor(target: EventTarget | null, boundary: HTMLElement): boolean {
  let node = target instanceof Element ? target : null
  while (node && node !== boundary) {
    if (node instanceof HTMLElement && node.scrollTop > 0) {
      const oy = getComputedStyle(node).overflowY
      if (oy === 'auto' || oy === 'scroll') return true
    }
    node = node.parentElement
  }
  return false
}

/**
 * Touch pull-to-refresh at the top of a timeline using rubber-band resistance
 * and spring release (replaces browser PTR disabled by overscroll-behavior: none).
 */
export function usePullToRefresh(
  container: Ref<HTMLElement | undefined>,
  getRefresh: () => (() => Promise<unknown>) | undefined,
) {
  const pullOffset = ref(0)
  const pullArmed = ref(false)
  const pullRefreshing = ref(false)

  const spring = createSpring(0, (v) => {
    pullOffset.value = v > 0.5 ? Math.round(v * 10) / 10 : 0
  })

  let state: 'idle' | 'pending' | 'pulling' = 'idle'
  let startX = 0
  let startY = 0
  let pullOriginY = 0
  let refreshGeneration = 0
  let boundEl: HTMLElement | undefined

  function resetAll() {
    refreshGeneration++
    state = 'idle'
    pullArmed.value = false
    pullRefreshing.value = false
    spring.set(0)
  }

  watch(sessionEpoch, resetAll, { flush: 'sync' })

  function onTouchStart(e: TouchEvent) {
    const refresh = getRefresh()
    const el = container.value
    if (!refresh || !el || pullRefreshing.value || e.touches.length !== 1) return
    if (window.scrollY > 1 || document.documentElement.classList.contains('has-sheet')) return
    if (isFormElement(e.target) || hasScrolledAncestor(e.target, el)) return
    const touch = e.touches[0]!
    startX = touch.clientX
    startY = touch.clientY
    state = 'pending'
  }

  function onTouchMove(e: TouchEvent) {
    if (state === 'idle') return
    if (e.touches.length !== 1) {
      cancelPull()
      return
    }
    const touch = e.touches[0]!
    const dx = touch.clientX - startX
    const dy = touch.clientY - startY

    if (state === 'pending') {
      if (Math.hypot(dx, dy) < PULL_HYSTERESIS) return
      // Yield immediately to horizontal swipes (e.g. PostCard swipe actions) or upward scroll.
      if (dy <= 0 || Math.abs(dx) * 1.2 >= dy || window.scrollY > 1) {
        state = 'idle'
        return
      }
      state = 'pulling'
      pullOriginY = touch.clientY
      spring.stop()
    }

    if (window.scrollY > 1) {
      cancelPull()
      return
    }

    const raw = Math.max(0, touch.clientY - pullOriginY)
    if (raw > 0 && e.cancelable) e.preventDefault()
    const offset = prefersReducedMotion()
      ? Math.min(PULL_REFRESH_THRESHOLD + 8, raw * 0.45)
      : rubberband(raw, PULL_RUBBER_DIMENSION, 0.55)
    spring.set(offset)

    const armed = offset >= PULL_REFRESH_THRESHOLD
    if (armed !== pullArmed.value) {
      pullArmed.value = armed
      if (armed) navigator.vibrate?.(8)
    }
  }

  function settleToZero() {
    if (prefersReducedMotion()) spring.set(0)
    else void spring.to(0, SPRING_DEFAULT)
  }

  function cancelPull() {
    const wasPulling = state === 'pulling'
    state = 'idle'
    pullArmed.value = false
    if (wasPulling && !pullRefreshing.value) settleToZero()
  }

  async function onTouchEnd() {
    if (state !== 'pulling') {
      state = 'idle'
      return
    }
    state = 'idle'
    const shouldRefresh = pullArmed.value && window.scrollY <= 1
    pullArmed.value = false
    const refresh = getRefresh()
    if (!shouldRefresh || !refresh) {
      settleToZero()
      return
    }

    const gen = ++refreshGeneration
    pullRefreshing.value = true
    if (prefersReducedMotion()) spring.set(PULL_HOLD_OFFSET)
    else void spring.to(PULL_HOLD_OFFSET, SPRING_DEFAULT)

    try {
      await refresh()
    } catch {
      // Timeline stores surface request errors on their own state.
    } finally {
      if (gen === refreshGeneration) {
        pullRefreshing.value = false
        settleToZero()
      }
    }
  }

  function bind(el: HTMLElement | undefined) {
    if (boundEl === el) return
    if (boundEl) {
      boundEl.removeEventListener('touchstart', onTouchStart)
      boundEl.removeEventListener('touchmove', onTouchMove)
      boundEl.removeEventListener('touchend', onTouchEnd)
      boundEl.removeEventListener('touchcancel', cancelPull)
    }
    boundEl = el
    if (el) {
      el.addEventListener('touchstart', onTouchStart, { passive: true })
      el.addEventListener('touchmove', onTouchMove, { passive: false })
      el.addEventListener('touchend', onTouchEnd, { passive: true })
      el.addEventListener('touchcancel', cancelPull, { passive: true })
    }
  }

  watch(container, el => bind(el))
  onMounted(() => bind(container.value))
  onBeforeUnmount(() => {
    bind(undefined)
    spring.stop()
  })

  return {
    pullOffset: readonly(pullOffset),
    pullArmed: readonly(pullArmed),
    pullRefreshing: readonly(pullRefreshing),
  }
}
