<script setup lang="ts" generic="T">
import { ref, computed, onMounted, onUnmounted } from 'vue'

interface Props {
  items: T[]
  itemHeight: number
  overscan?: number
  keyField?: keyof T
}

const props = withDefaults(defineProps<Props>(), {
  overscan: 3,
  keyField: 'id' as keyof T,
})

const emit = defineEmits<{
  scroll: [event: Event]
  scrollBottom: []
}>()

const containerRef = ref<HTMLElement | null>(null)
const scrollTop = ref(0)
const containerHeight = ref(0)

// Guard against zero/negative itemHeight to prevent division by zero
// (Infinity/NaN in visibleRange) and invalid CSS height values
const safeItemHeight = computed(() => Math.max(1, props.itemHeight))

// Calculate visible range
const visibleRange = computed(() => {
  const start = Math.floor(scrollTop.value / safeItemHeight.value)
  const visibleCount = Math.ceil(containerHeight.value / safeItemHeight.value)
  const end = start + visibleCount

  // Add overscan for smoother scrolling
  const startWithOverscan = Math.max(0, start - props.overscan)
  const endWithOverscan = Math.min(props.items.length, end + props.overscan)

  return {
    start: startWithOverscan,
    end: endWithOverscan,
  }
})

// Pre-slice visible items once — avoids O(n²) indexOf in template
const visibleItems = computed(() =>
  props.items.slice(visibleRange.value.start, visibleRange.value.end)
)

// Total height of the list
const totalHeight = computed(() => props.items.length * safeItemHeight.value)

// Resolve the key for a visible item. When the configured keyField is missing
// on an item (undefined), fall back to the index to avoid all rows sharing the
// duplicate key "undefined", which causes Vue render warnings and state errors.
function getItemKey(item: T, index: number): string | number {
  const value = (item as Record<string, unknown>)[props.keyField as string]
  return value === undefined || value === null ? index : (value as string | number)
}

// Container style
const containerStyle = computed(() => ({
  height: `${totalHeight.value}px`,
  position: 'relative' as const,
}))

// Wrapper style — translate by start offset
const wrapperStyle = computed(() => ({
  transform: `translateY(${visibleRange.value.start * safeItemHeight.value}px)`,
  willChange: 'transform',
}))

// Check if we should trigger scrollBottom event
let lastScrollTop = 0
let rafId: number | null = null

function handleScroll(e: Event) {
  const target = e.target as HTMLElement
  if (rafId !== null) return
  rafId = requestAnimationFrame(() => {
    scrollTop.value = target.scrollTop
    containerHeight.value = target.clientHeight
    rafId = null

    emit('scroll', e)

    // Check if scrolled to bottom
    const { scrollHeight, scrollTop: currentScrollTop, clientHeight } = target
    const isAtBottom = scrollHeight - currentScrollTop - clientHeight < safeItemHeight.value * 2

    if (isAtBottom && lastScrollTop < currentScrollTop) {
      emit('scrollBottom')
    }
    lastScrollTop = currentScrollTop
  })
}

// Set container height on mount (avoids null ref in immediate watch)
let resizeObserver: ResizeObserver | null = null

onMounted(() => {
  if (containerRef.value) {
    containerHeight.value = containerRef.value.clientHeight
    // Observe size changes for responsive behavior
    if ('ResizeObserver' in window) {
      resizeObserver = new ResizeObserver(() => {
        if (containerRef.value) {
          containerHeight.value = containerRef.value.clientHeight
        }
      })
      resizeObserver.observe(containerRef.value)
    }
  }
})

onUnmounted(() => {
  if (rafId !== null) {
    cancelAnimationFrame(rafId)
    rafId = null
  }
  resizeObserver?.disconnect()
})

// Expose methods
function scrollToIndex(index: number) {
  if (containerRef.value) {
    containerRef.value.scrollTop = index * safeItemHeight.value
  }
}

function scrollToBottom() {
  if (props.items.length === 0) return
  scrollToIndex(props.items.length - 1)
}

defineExpose({
  scrollToIndex,
  scrollToBottom,
  containerRef,
})
</script>


<template>
  <div
    ref="containerRef"
    class="virtual-list-container"
    @scroll.passive="handleScroll"
  >
    <div :style="containerStyle">
      <div :style="wrapperStyle" class="virtual-list-wrapper">
        <div
          v-for="(item, idx) in visibleItems"
          :key="getItemKey(item, visibleRange.start + idx)"
          class="virtual-list-item"
          :style="{ height: `${safeItemHeight}px` }"
        >
          <slot :item="item" :index="visibleRange.start + idx">
            {{ item }}
          </slot>
        </div>
      </div>
    </div>
  </div>
</template>


<style scoped>
.virtual-list-container {
  overflow-y: auto;
  overflow-x: hidden;
  height: 100%;
  position: relative;
}

.virtual-list-wrapper {
  position: relative;
}

.virtual-list-item {
  box-sizing: border-box;
  overflow: hidden;
}
</style>
