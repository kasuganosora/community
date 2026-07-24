import { ref, onMounted, onUnmounted, type Ref } from 'vue'

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
  const { src, placeholder, rootMargin = '50px', threshold = 0.01 } = options

  const isLoaded = ref(false)
  const isInView = ref(false)
  const currentSrc = ref(placeholder || '')
  const error = ref<Error | null>(null)

  let observer: IntersectionObserver | null = null
  let hasTriggered = false
  let preloadImg: HTMLImageElement | null = null

  function loadImage() {
    if (!src || hasTriggered) return
    hasTriggered = true

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

  onMounted(() => {
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
    if (preloadImg) {
      preloadImg.onload = null
      preloadImg.onerror = null
      preloadImg.src = ''
      preloadImg = null
    }
  })
  function reload() {
    hasTriggered = false
    isLoaded.value = false
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
  const { src, rootMargin = '50px', threshold = 0.01 } = options

  const isLoaded = ref(false)
  let observer: IntersectionObserver | null = null
  let hasTriggered = false
  let preloadImg: HTMLImageElement | null = null

  function loadBackground() {
    if (!src || hasTriggered) return
    hasTriggered = true

    preloadImg = new Image()
    preloadImg.src = src

    preloadImg.onload = () => {
      if (elementRef.value) {
        // Sanitize URL to prevent CSS injection
        const sanitized = src.replace(/["'\\]/g, '')
        elementRef.value.style.backgroundImage = `url("${sanitized}")`
      }
      isLoaded.value = true
    }

    preloadImg.onerror = () => {
      console.error('Background image load error:', src)
    }
  }

  onMounted(() => {
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
