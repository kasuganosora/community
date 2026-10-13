<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { isGroupReference } from '@/domains/trending/domain/groupSearch'

const props = defineProps<{
  /** On Explore, start searching as the query is typed. The rail still waits for submit. */
  live?: boolean
}>()

const router = useRouter()
const query = ref('')
const root = ref<HTMLElement | null>(null)
const edged = ref(false)

let scroller: HTMLElement | Window | null = null
let liveTimer = 0

function scrollParent(el: HTMLElement): HTMLElement | Window {
  let parent = el.parentElement
  while (parent && parent !== document.documentElement) {
    const overflow = getComputedStyle(parent).overflowY
    if (overflow === 'auto' || overflow === 'scroll') return parent
    parent = parent.parentElement
  }
  return window
}

function updateEdge() {
  if (!scroller) return
  const top = scroller instanceof Window ? window.scrollY : scroller.scrollTop
  edged.value = top > 2
}

function handleSearch() {
  const trimmedQuery = query.value.trim()
  if (!trimmedQuery) return
  router.push({ name: 'search', query: { q: trimmedQuery, type: isGroupReference(trimmedQuery) ? 'groups' : undefined } }).catch(() => {})
}

watch(query, (value) => {
  window.clearTimeout(liveTimer)
  if (!props.live) return
  const trimmed = value.trim()
  if (!trimmed) return
  liveTimer = window.setTimeout(handleSearch, 400)
})

onMounted(() => {
  if (!root.value) return
  scroller = scrollParent(root.value)
  updateEdge()
  scroller.addEventListener('scroll', updateEdge, { passive: true })
})

onBeforeUnmount(() => {
  window.clearTimeout(liveTimer)
  scroller?.removeEventListener('scroll', updateEdge)
})
</script>

<template>
  <form ref="root" class="search-box" :class="{ 'search-box--edge': edged }" role="search" @submit.prevent="handleSearch">
    <t-input
      v-model="query"
      placeholder="搜索帖子、用户或群组"
      aria-label="搜索"
      clearable
    >
      <template #prefixIcon>
        <t-icon name="search" />
      </template>
    </t-input>
    <button type="submit" class="btn btn--primary btn--sm search-box__submit" :disabled="!query.trim()">搜索</button>
  </form>
</template>

<style scoped>
/* Sticks with the page nav bar: same height, same material, so the rail's
   search field and the column's title read as one floating layer. */
.search-box {
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: var(--bar-height);
  margin: 0 calc(-1 * var(--space-2));
  padding: var(--space-2);
  background: var(--material-bar);
  -webkit-backdrop-filter: var(--material-blur-bar);
  backdrop-filter: var(--material-blur-bar);
  box-shadow: 0 var(--hairline) 0 transparent;
  transition: box-shadow var(--duration-fast) var(--ease-out);
}

/* Hairline only once the rail's content has scrolled under the field. */
.search-box--edge {
  box-shadow: 0 var(--hairline) 0 var(--material-bar-border);
}

.search-box :deep(.t-input__wrap) {
  flex: 1;
  min-width: 0;
}

.search-box :deep(.t-input) {
  height: var(--hit-target);
  border-radius: var(--radius-full);
  padding: 0 var(--space-4);
}

.search-box__submit {
  flex: 0 0 auto;
}
</style>
