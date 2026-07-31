import { ref, computed, onMounted, onUnmounted, type Ref } from 'vue'

interface UseInfiniteScrollOptions {
  threshold?: number
  disabled?: Ref<boolean> | boolean
  immediate?: boolean
}

export function useInfiniteScroll(
  elementRef: Ref<HTMLElement | null>,
  callback: () => void | Promise<void>,
  options: UseInfiniteScrollOptions = {}
) {
  const { threshold = 100, disabled = false, immediate = false } = options

  const isLoading = ref(false)
  const hasError = ref(false)
  const error = ref<Error | null>(null)
  let isTriggered = false

  const isDisabled = computed(() => {
    return typeof disabled === 'boolean' ? disabled : disabled.value
  })

  let retryTimer: ReturnType<typeof setTimeout> | null = null
  let retryCount = 0
  const MAX_RETRIES = 3

  async function checkAndLoad() {
    if (isLoading.value || isDisabled.value || isTriggered) return

    const el = elementRef.value
    if (!el) return

    const { scrollTop, scrollHeight, clientHeight } = el
    const remaining = scrollHeight - scrollTop - clientHeight

    if (remaining < threshold) {
      isTriggered = true
      isLoading.value = true
      hasError.value = false
      error.value = null

      try {
        await callback()
        retryCount = 0
        // Reset trigger after a short delay to allow scroll position to update
        retryTimer = setTimeout(() => {
          isTriggered = false
        }, 100)
      } catch (err) {
        hasError.value = true
        error.value = err instanceof Error ? err : new Error(String(err))
        // Only retry on error, not on success
        if (retryCount < MAX_RETRIES) {
          retryCount++
          retryTimer = setTimeout(() => {
            isTriggered = false
            checkAndLoad()
          }, 100)
        } else {
          isTriggered = false
          retryCount = 0
        }
      } finally {
        isLoading.value = false
      }
    }
  }

  function handleScroll() {
    checkAndLoad()
  }

  // Setup scroll listener
  let scrollEl: HTMLElement | Window | null = null

  onMounted(() => {
    scrollEl = elementRef.value
    if (scrollEl) {
      scrollEl.addEventListener('scroll', handleScroll, { passive: true })

      if (immediate) {
        checkAndLoad()
      }
    }
  })

  onUnmounted(() => {
    if (scrollEl) {
      scrollEl.removeEventListener('scroll', handleScroll)
    }
    if (retryTimer) {
      clearTimeout(retryTimer)
      retryTimer = null
    }
  })

  // Manual check
  function check() {
    checkAndLoad()
  }

  // Reset state
  function reset() {
    isLoading.value = false
    hasError.value = false
    error.value = null
    isTriggered = false
  }

  return {
    isLoading: computed(() => isLoading.value),
    hasError: computed(() => hasError.value),
    error: computed(() => error.value),
    check,
    reset,
  }
}

// Alternative version using IntersectionObserver (more performant)
export function useInfiniteScrollObserver(
  sentinelRef: Ref<HTMLElement | null>,
  callback: () => void | Promise<void>,
  options: Omit<UseInfiniteScrollOptions, 'immediate'> = {}
) {
  const { disabled = false } = options

  const isLoading = ref(false)
  const hasError = ref(false)
  const error = ref<Error | null>(null)
  let observer: IntersectionObserver | null = null

  const isDisabled = computed(() => {
    return typeof disabled === 'boolean' ? disabled : disabled.value
  })

  async function handleIntersection(entries: IntersectionObserverEntry[]) {
    const entry = entries[0]
    if (!entry?.isIntersecting || isLoading.value || isDisabled.value) return

    // Temporarily unobserve to prevent duplicate triggers during DOM update
    if (sentinelRef.value) {
      observer?.unobserve(sentinelRef.value)
    }
    isLoading.value = true
    hasError.value = false
    error.value = null

    try {
      await callback()
    } catch (err) {
      hasError.value = true
      error.value = err instanceof Error ? err : new Error(String(err))
    } finally {
      isLoading.value = false
      // Re-observe after DOM settles to catch further scrolls
      if (sentinelRef.value) {
        observer?.observe(sentinelRef.value)
      }
    }
  }

  onMounted(() => {
    if (!sentinelRef.value) return

    observer = new IntersectionObserver(handleIntersection, {
      root: null,
      rootMargin: '100px',
      threshold: 0,
    })

    observer.observe(sentinelRef.value)
  })

  onUnmounted(() => {
    observer?.disconnect()
  })

  return {
    isLoading: computed(() => isLoading.value),
    hasError: computed(() => hasError.value),
    error: computed(() => error.value),
  }
}
