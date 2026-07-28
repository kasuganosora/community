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

  function cleanupPreloadImg() {
    if (preloadImg) {
      preloadImg.onload = null
      preloadImg.onerror = null
      preloadImg.src = ''
      preloadImg = null
    }
  }

  function loadImage() {
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

    preloadImg.onerror = (e) => {
      error.value = new Error(`Failed to load image: ${src}`)
      console.error('Image load error:', e)
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
  })

  function reload() {
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
  let observer: IntersectionObserver | null = null
  let hasTriggered = false
  let preloadImg: HTMLImageElement | null = null
  let isMounted = false

  function loadBackground() {
    const src = getSrc()
    if (!src || hasTriggered) return
    hasTriggered = true

    if (preloadImg) {
      preloadImg.onload = null
      preloadImg.onerror = null
      preloadImg.src = ''
    }

    preloadImg = new Image()
    preloadImg.src = src

    preloadImg.onload = () => {
      if (elementRef.value) {
        // Sanitize URL to prevent CSS injection — escape quotes and parens
        const safeUrl = src.replace(/["'()\\]/g, '\\$&')
        elementRef.value.style.backgroundImage = `url("${safeUrl}")`
      }
      isLoaded.value = true
    }

    preloadImg.onerror = () => {
      console.error('Background image load error:', src)
    }
  }

  // Watch for src changes
  watch(() => options.src, (newSrc, oldSrc) => {
    if (isMounted && newSrc && newSrc !== oldSrc) {
      hasTriggered = false
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
    if (preloadImg) {
      preloadImg.onload = null
      preloadImg.onerror = null
      preloadImg.src = ''
      preloadImg = null
    }
  })

  return { isLoaded }
}
