import { ref, computed, onMounted, onUnmounted, watch, type Ref } from 'vue'

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
  let isExhausted = false

  async function checkAndLoad(): Promise<void> {
    if (isLoading.value || isDisabled.value || isTriggered || isExhausted) return

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
        clearRetryTimer()
        retryTimer = setTimeout(() => {
          isTriggered = false
          retryTimer = null
        }, 100)
      } catch (err) {
        hasError.value = true
        error.value = err instanceof Error ? err : new Error(String(err))
        // Retry up to MAX_RETRIES times; after that, stop to prevent infinite loop
        if (retryCount < MAX_RETRIES) {
          retryCount++
          clearRetryTimer()
          retryTimer = setTimeout(() => {
            isTriggered = false
            retryTimer = null
            checkAndLoad()
          }, 100 * retryCount)
        } else {
          isExhausted = true
          isTriggered = false
        }
      } finally {
        isLoading.value = false
      }
    }
  }

  function handleScroll(): void {
    checkAndLoad()
  }

  function clearRetryTimer(): void {
    if (retryTimer) {
      clearTimeout(retryTimer)
      retryTimer = null
    }
  }

  // Setup scroll listener
  let scrollEl: HTMLElement | null = null

  // Watch for dynamic element changes (conditional rendering, async loaded elements)
  watch(elementRef, (newEl, oldEl) => {
    if (oldEl) {
      oldEl.removeEventListener('scroll', handleScroll)
    }
    if (newEl) {
      newEl.addEventListener('scroll', handleScroll, { passive: true })
      if (immediate) {
        checkAndLoad()
      }
    }
    scrollEl = newEl
  })

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
    clearRetryTimer()
  })

  // Manual check
  function check(): void {
    // Reset exhaustion state on manual check — user explicitly requested
    isExhausted = false
    retryCount = 0
    checkAndLoad()
  }

  // Reset state
  function reset(): void {
    isLoading.value = false
    hasError.value = false
    error.value = null
    isTriggered = false
    isExhausted = false
    retryCount = 0
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
  const { threshold: _threshold = 100, disabled = false } = options
  void _threshold // threshold is handled via rootMargin below

  const isLoading = ref(false)
  const hasError = ref(false)
  const error = ref<Error | null>(null)
  let observer: IntersectionObserver | null = null
  let retryCount = 0
  const MAX_RETRIES = 3

  const isDisabled = computed(() => {
    return typeof disabled === 'boolean' ? disabled : disabled.value
  })

  async function handleIntersection(entries: IntersectionObserverEntry[]): Promise<void> {
    const entry = entries[0]
    if (!entry?.isIntersecting || isLoading.value || isDisabled.value) return

    // Temporarily unobserve to prevent duplicate triggers during DOM update
    if (sentinelRef.value) {
      observer?.unobserve(sentinelRef.value)
    }
    isLoading.value = true
    hasError.value = false
    error.value = null

    // Track whether we should re-observe after the callback settles.
    // Set to false when retries are exhausted to break the infinite loop.
    let shouldReobserve = true

    try {
      await callback()
      retryCount = 0
    } catch (err) {
      hasError.value = true
      error.value = err instanceof Error ? err : new Error(String(err))

      // If we've exhausted retries, stop re-observing to prevent infinite loop
      if (retryCount >= MAX_RETRIES) {
        shouldReobserve = false
      } else {
        retryCount++
      }
    } finally {
      isLoading.value = false
      // Re-observe only if we haven't exhausted retries
      if (shouldReobserve && sentinelRef.value) {
        observer?.observe(sentinelRef.value)
      }
    }
  }

  onMounted(() => {
    if (!sentinelRef.value) return

    observer = new IntersectionObserver(
      (entries) => { handleIntersection(entries) },
      {
        root: null,
        rootMargin: '100px',
        threshold: 0,
      }
    )

    observer.observe(sentinelRef.value)
  })

  function reset(): void {
    retryCount = 0
    hasError.value = false
    error.value = null
    // Re-observe sentinel to resume triggering
    if (sentinelRef.value && observer) {
      observer.observe(sentinelRef.value)
    }
  }

  onUnmounted(() => {
    observer?.disconnect()
    observer = null
  })

  return {
    isLoading: computed(() => isLoading.value),
    hasError: computed(() => hasError.value),
    error: computed(() => error.value),
    reset,
  }
}
