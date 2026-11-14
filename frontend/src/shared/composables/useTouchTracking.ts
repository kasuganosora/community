import { onScopeDispose, readonly, ref, watch, getCurrentScope } from 'vue'

const active = ref(false)
let consumers = 0
let listening = false

function onTouchStart() {
  active.value = true
}

function onTouchFinish(e: Event) {
  const touches = (e as TouchEvent).touches
  if (!touches || touches.length === 0) active.value = false
}

function clearTracking() {
  active.value = false
}

function attach() {
  if (listening || typeof window === 'undefined') return
  listening = true
  window.addEventListener('touchstart', onTouchStart, { capture: true, passive: true })
  window.addEventListener('touchend', onTouchFinish, { capture: true, passive: true })
  window.addEventListener('touchcancel', clearTracking, { capture: true, passive: true })
  window.addEventListener('blur', clearTracking)
  document.addEventListener('visibilitychange', clearTracking)
}

function detach() {
  if (!listening || typeof window === 'undefined') return
  listening = false
  active.value = false
  window.removeEventListener('touchstart', onTouchStart, true)
  window.removeEventListener('touchend', onTouchFinish, true)
  window.removeEventListener('touchcancel', clearTracking, true)
  window.removeEventListener('blur', clearTracking)
  document.removeEventListener('visibilitychange', clearTracking)
}

/**
 * Tracks whether a finger is actively touching the screen so background stream
 * updates wait until release instead of mutating rows mid-gesture on mobile.
 */
export function useTouchTracking() {
  consumers++
  attach()
  if (getCurrentScope()) {
    onScopeDispose(() => {
      consumers = Math.max(0, consumers - 1)
      if (consumers === 0) detach()
    })
  }
  return { isTouchTracking: readonly(active) }
}

/**
 * Reveal streamed/polled newer timeline posts automatically once ready, while
 * deferring the DOM commit if the user's finger is currently dragging.
 */
export function useAutoTimelineReveal(key: () => string, reveal: () => unknown) {
  const { isTouchTracking } = useTouchTracking()
  watch(
    () => (isTouchTracking.value ? '' : key()),
    (token) => {
      if (token) void Promise.resolve(reveal()).catch(() => {})
    },
    { flush: 'post' },
  )
  return { isTouchTracking }
}
