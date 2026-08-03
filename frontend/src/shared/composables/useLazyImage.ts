import { ref, onMounted, onUnmounted, watch, type Ref } from 'vue'

interface UseLazyImageOptions {
  rootMargin?: string
  threshold?: number
  src: string
  placeholder?: string
}

export function useLazyImage(
  imageRef: Ref<HTMLImageElement | null>,
  options: UseLazyImageOptions
) {
  const { placeholder, rootMargin = '50px', threshold = 0.01 } = options
  // Use a getter to maintain reactivity — options.src may change dynamically
  const getSrc = () => options.src

  const isLoaded = ref(false)
  const isInView = ref(false)
  const currentSrc = ref(placeholder || '')
  const error = ref<Error | null>(null)

  let observer: IntersectionObserver | null = null
  let hasTriggered = false
  let preloadImg: HTMLImageElement | null = null
  let isMounted = false

  function cleanupPreloadImg(): void {
    if (preloadImg) {
      preloadImg.onload = null
      preloadImg.onerror = null
      preloadImg.src = ''
      preloadImg = null
    }
  }

  function loadImage(): void {
    const src = getSrc()
    if (!src || hasTriggered) return
    hasTriggered = true

    // Clean up any previous preload image to prevent callback cross-talk
    cleanupPreloadImg()

    preloadImg = new Image()
    preloadImg.src = src

    preloadImg.onload = () => {
      currentSrc.value = src
      isLoaded.value = true
      error.value = null
    }

    preloadImg.onerror = () => {
      error.value = new Error(`Failed to load image: ${src}`)
    }
  }

  // Watch for src changes and reload
  watch(() => options.src, (newSrc, oldSrc) => {
    if (isMounted && newSrc && newSrc !== oldSrc) {
      reload()
    }
  })

  onMounted(() => {
    isMounted = true
    if (!imageRef.value) return

    // Check if IntersectionObserver is supported
    if (!('IntersectionObserver' in window)) {
      // Fallback: load immediately
      loadImage()
      return
    }

    observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0]
        if (!entry) return
        
        isInView.value = entry.isIntersecting

        if (entry.isIntersecting) {
          loadImage()
          // Disconnect after triggering load
          observer?.disconnect()
        }
      },
      {
        rootMargin,
        threshold,
      }
    )

    observer.observe(imageRef.value)
  })

  onUnmounted(() => {
    observer?.disconnect()
    observer = null
    cleanupPreloadImg()
    isMounted = false
  })

  function reload(): void {
    hasTriggered = false
    isLoaded.value = false
    cleanupPreloadImg()
    loadImage()
  }

  return {
    isLoaded,
    isInView,
    currentSrc,
    error,
    reload,
  }
}

// Simple version for background images
export function useLazyBackground(
  elementRef: Ref<HTMLElement | null>,
  options: UseLazyImageOptions
) {
  const { rootMargin = '50px', threshold = 0.01 } = options
  const getSrc = () => options.src

  const isLoaded = ref(false)
  const error = ref<Error | null>(null)
  let observer: IntersectionObserver | null = null
  let hasTriggered = false
  let preloadImg: HTMLImageElement | null = null
  let isMounted = false

  function cleanupPreloadImg(): void {
    if (preloadImg) {
      preloadImg.onload = null
      preloadImg.onerror = null
      preloadImg.src = ''
      preloadImg = null
    }
  }

  function loadBackground(): void {
    const src = getSrc()
    if (!src || hasTriggered) return
    hasTriggered = true

    cleanupPreloadImg()

    preloadImg = new Image()
    preloadImg.src = src

    preloadImg.onload = () => {
      if (elementRef.value) {
        // Sanitize URL to prevent CSS injection — escape quotes and parens
        const safeUrl = src.replace(/["'()\\\n\r]/g, '\\$&')
        elementRef.value.style.backgroundImage = `url("${safeUrl}")`
      }
      isLoaded.value = true
      error.value = null
    }

    preloadImg.onerror = () => {
      error.value = new Error(`Failed to load background image: ${src}`)
    }
  }

  // Watch for src changes
  watch(() => options.src, (newSrc, oldSrc) => {
    if (isMounted && newSrc && newSrc !== oldSrc) {
      hasTriggered = false
      isLoaded.value = false
      cleanupPreloadImg()
      loadBackground()
    }
  })

  onMounted(() => {
    isMounted = true
    if (!elementRef.value) return

    if (!('IntersectionObserver' in window)) {
      loadBackground()
      return
    }

    observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0]
        if (entry?.isIntersecting) {
          loadBackground()
          observer?.disconnect()
        }
      },
      { rootMargin, threshold }
    )

    observer.observe(elementRef.value)
  })

  onUnmounted(() => {
    observer?.disconnect()
    observer = null
    cleanupPreloadImg()
    isMounted = false
  })

  return { isLoaded, error }
}
