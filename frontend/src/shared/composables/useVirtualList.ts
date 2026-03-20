import { ref, computed, watch, type Ref } from 'vue'

interface UseVirtualListOptions {
  itemHeight: number
  overscan?: number
  containerHeight: number
}

interface VirtualItem<T> {
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
  const { itemHeight, overscan = 5, containerHeight } = options

  const scrollTop = ref(0)
  const containerRef = ref<HTMLElement | null>(null)

  // Calculate visible range
  const visibleRange = computed(() => {
    const start = Math.floor(scrollTop.value / itemHeight)
    const visibleCount = Math.ceil(containerHeight / itemHeight)
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

  // Offset style for the container
  const containerStyle = computed(() => ({
    height: `${totalHeight.value}px`,
    position: 'relative' as const,
  }))

  // Wrapper style to offset visible items
  const wrapperStyle = computed(() => ({
    transform: `translateY(${visibleRange.value.start * itemHeight}px)`,
  }))

  // Scroll handler
  function handleScroll(e: Event) {
    const target = e.target as HTMLElement
    scrollTop.value = target.scrollTop
  }

  // Scroll to specific index
  function scrollToIndex(index: number) {
    if (containerRef.value) {
      containerRef.value.scrollTop = index * itemHeight
    }
  }

  // Scroll to bottom
  function scrollToBottom() {
    scrollToIndex(list.value.length - 1)
  }

  // Watch for list changes and scroll to bottom if already at bottom
  let wasAtBottom = false
  watch(
    () => list.value.length,
    (newLen, oldLen) => {
      if (newLen > oldLen && wasAtBottom) {
        scrollToBottom()
      }
    }
  )

  // Check if currently at bottom (for auto-scroll new items)
  function checkIsAtBottom() {
    if (!containerRef.value) return false
    const { scrollTop, scrollHeight, clientHeight } = containerRef.value
    wasAtBottom = scrollHeight - scrollTop - clientHeight < itemHeight
    return wasAtBottom
  }

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
