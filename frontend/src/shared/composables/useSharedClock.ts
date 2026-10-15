import { ref, onMounted, onUnmounted } from 'vue'

/**
 * Shared clock that updates every 60 seconds while the page is visible.
 * It stops while hidden (battery) and catches up immediately on return.
 * All components using this composable share a single timer,
 * avoiding N separate setInterval timers for N RelativeTime instances.
 */
const sharedNow = ref(Date.now())
let consumerCount = 0
let timer: ReturnType<typeof setInterval> | null = null

export const CLOCK_INTERVAL_MS = 60_000

function tick() {
  sharedNow.value = Date.now()
}

function onVisibility() {
  if (document.hidden) {
    if (timer) { clearInterval(timer); timer = null }
  } else if (consumerCount > 0 && !timer) {
    tick()
    timer = setInterval(tick, CLOCK_INTERVAL_MS)
  }
}

function startTimer() {
  if (consumerCount === 1 && typeof document !== 'undefined') document.addEventListener('visibilitychange', onVisibility)
  if (timer) return
  tick()
  if (typeof document !== 'undefined' && document.hidden) return
  timer = setInterval(tick, CLOCK_INTERVAL_MS)
}

function stopTimer() {
  if (consumerCount !== 0) return
  if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', onVisibility)
  if (timer) {
    clearInterval(timer)
    timer = null
  }
}

// HMR support — clean up timer when module is hot-replaced
if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
    consumerCount = 0
  })
}

export function useSharedClock() {
  onMounted(() => {
    consumerCount++
    startTimer()
  })

  onUnmounted(() => {
    consumerCount = Math.max(0, consumerCount - 1)
    stopTimer()
  })

  return sharedNow
}
