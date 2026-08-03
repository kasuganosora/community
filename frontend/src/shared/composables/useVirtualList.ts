import { ref, computed, watch, onUnmounted, nextTick, type Ref, type MaybeRefOrGetter, toRef } from 'vue'

interface UseVirtualListOptions {
  itemHeight: number
  overscan?: number
  containerHeight: MaybeRefOrGetter<number>
}

export interface VirtualItem<T> {
  data: T
  index: number
  style: {
    position: 'absolute'
    top: string
    height: string
    width: string
  }
}

export function useVirtualList<T>(
  list: Ref<T[]>,
  options: UseVirtualListOptions
) {
  const { itemHeight, overscan = 5 } = options
  const containerHeightRef = toRef(options.containerHeight)

  const scrollTop = ref(0)
  const containerRef = ref<HTMLElement | null>(null)

  // Calculate visible range
  const visibleRange = computed(() => {
    const start = Math.floor(scrollTop.value / itemHeight)
    const visibleCount = Math.ceil(containerHeightRef.value / itemHeight)
    const end = start + visibleCount

    // Add overscan for smoother scrolling
    const startWithOverscan = Math.max(0, start - overscan)
    const endWithOverscan = Math.min(list.value.length, end + overscan)

    return {
      start: startWithOverscan,
      end: endWithOverscan,
      startOriginal: start,
    }
  })

  // Generate virtual items with positioning styles
  // Items use top relative to container (not wrapper), avoiding double offset
  const virtualItems = computed<VirtualItem<T>[]>(() => {
    const { start, end } = visibleRange.value
    const items: VirtualItem<T>[] = []

    for (let i = start; i < end; i++) {
      const item = list.value[i]
      if (item) {
        items.push({
          data: item,
          index: i,
          style: {
            position: 'absolute',
            top: `${i * itemHeight}px`,
            height: `${itemHeight}px`,
            width: '100%',
          },
        })
      }
    }

    return items
  })

  // Total height of the list
  const totalHeight = computed(() => list.value.length * itemHeight)

  // Container style
  const containerStyle = computed(() => ({
    height: `${totalHeight.value}px`,
    position: 'relative' as const,
  }))

  // Wrapper style is intentionally empty — items use absolute positioning
  // relative to containerStyle. Using transform would create a new containing
  // block and cause double offset.
  const wrapperStyle = computed(() => ({}))

  // Scroll handler — throttled with rAF for performance
  let rafId: number | null = null
  let wasAtBottom = false

  function handleScroll(e: Event) {
    if (rafId !== null) return
    const target = e.target as HTMLElement
    rafId = requestAnimationFrame(() => {
      scrollTop.value = target.scrollTop
      // Auto-update wasAtBottom so consumers don't need to call checkIsAtBottom manually
      const { scrollHeight, clientHeight } = target
      wasAtBottom = scrollHeight - target.scrollTop - clientHeight < itemHeight
      rafId = null
    })
  }

  // Scroll to specific index
  function scrollToIndex(index: number) {
    if (containerRef.value) {
      containerRef.value.scrollTop = index * itemHeight
    }
  }

  // Scroll to bottom
  function scrollToBottom() {
    if (list.value.length === 0) return
    scrollToIndex(list.value.length - 1)
  }

  // Watch for list changes and scroll to bottom if already at bottom
  watch(
    () => list.value.length,
    (newLen, oldLen) => {
      if (newLen > oldLen && wasAtBottom) {
        // Wait for DOM to update so scrollHeight reflects new items
        nextTick(() => scrollToBottom())
      }
    }
  )

  // Check if currently at bottom (for auto-scroll new items)
  function checkIsAtBottom() {
    if (!containerRef.value) return false
    const { scrollTop: domScrollTop, scrollHeight, clientHeight } = containerRef.value
    wasAtBottom = scrollHeight - domScrollTop - clientHeight < itemHeight
    return wasAtBottom
  }

  onUnmounted(() => {
    if (rafId !== null) {
      cancelAnimationFrame(rafId)
      rafId = null
    }
  })

  return {
    containerRef,
    virtualItems,
    containerStyle,
    wrapperStyle,
    totalHeight,
    handleScroll,
    scrollToIndex,
    scrollToBottom,
    checkIsAtBottom,
    visibleRange,
  }
}
