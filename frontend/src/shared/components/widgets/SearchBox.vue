<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { isGroupReference } from '@/domains/trending/domain/groupSearch'

const props = withDefaults(defineProps<{
  /** On Explore, start searching as the query is typed. The rail still waits for submit. */
  live?: boolean
  searchType?: 'groups'
  placeholder?: string
  allowEmpty?: boolean
  /** Reuse the same field inside a page instead of a sticky sidebar. */
  embedded?: boolean
  /** Search results owns refresh/navigation while reusing this input. */
  navigate?: boolean
}>(), { navigate: true })
const emit = defineEmits<{ search: [query: string] }>()

const router = useRouter()
const query = defineModel<string>({ default: '' })
const root = ref<HTMLElement | null>(null)
const edged = ref(false)

let scroller: HTMLElement | Window | null = null
let liveTimer = 0
const composing = ref(false)

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
  window.clearTimeout(liveTimer)
  if (composing.value) return
  const trimmedQuery = query.value.trim()
  if (!trimmedQuery && !props.allowEmpty) return
  emit('search', trimmedQuery)
  if (props.navigate === false) return
  router.push({ name: 'search', query: { q: trimmedQuery || undefined, type: isGroupReference(trimmedQuery) ? 'groups' : props.searchType } }).catch(() => {})
}

function scheduleSearch() {
  window.clearTimeout(liveTimer)
  if (!props.live || composing.value || !query.value.trim()) return
  liveTimer = window.setTimeout(handleSearch, 400)
}
watch(query, scheduleSearch)

onMounted(() => {
  if (!root.value || props.embedded) return
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
  <form ref="root" class="search-box" :class="{ 'search-box--edge': edged, 'search-box--embedded': embedded }" role="search" @submit.prevent="handleSearch" @compositionstart="composing = true" @compositionend="composing = false; scheduleSearch()">
    <t-input
      v-model="query"
      :placeholder="placeholder || '搜索帖子、用户、话题或群组'"
      aria-label="搜索"
      clearable
    >
      <template #prefixIcon>
        <t-icon name="search" />
      </template>
    </t-input>
    <button type="submit" class="btn btn--primary btn--sm search-box__submit" :disabled="!query.trim() && !allowEmpty">搜索</button>
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

.search-box--embedded {
  position: static;
  z-index: auto;
  min-height: 0;
  margin: 0;
  padding: 0;
  background: transparent;
  -webkit-backdrop-filter: none;
  backdrop-filter: none;
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
  min-height: var(--hit-target);
}
</style>
