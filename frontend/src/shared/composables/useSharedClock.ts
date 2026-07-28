import { ref, onMounted, onUnmounted } from 'vue'

/**
 * Shared clock that updates every 30 seconds.
 * All components using this composable share a single timer,
 * avoiding N separate setInterval timers for N RelativeTime instances.
 */
const sharedNow = ref(Date.now())
let consumerCount = 0
let timer: ReturnType<typeof setInterval> | null = null

function startTimer() {
  if (timer) return
  sharedNow.value = Date.now()
  timer = setInterval(() => {
    sharedNow.value = Date.now()
  }, 30_000)
}

function stopTimer() {
  if (timer && consumerCount === 0) {
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
