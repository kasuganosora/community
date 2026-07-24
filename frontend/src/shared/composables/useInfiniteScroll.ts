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
      } catch (err) {
        hasError.value = true
        error.value = err instanceof Error ? err : new Error(String(err))
      } finally {
        isLoading.value = false
        // Reset trigger after a delay to allow scroll position to update
        retryTimer = setTimeout(() => {
          isTriggered = false
          // Check again in case we're still near bottom
          checkAndLoad()
        }, 100)
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
