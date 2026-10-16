<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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
const inputControl = ref<HTMLInputElement | null>(null)
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

async function clearSearch() {
  window.clearTimeout(liveTimer)
  query.value = ''
  await nextTick()
  inputControl.value?.focus()
}

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
  <form ref="root" class="search-box" :class="{ 'search-box--edge': edged, 'search-box--embedded': embedded, 'search-box--has-query': !!query }" role="search" @submit.prevent="handleSearch" @compositionstart="composing = true" @compositionend="composing = false; scheduleSearch()">
    <!-- Native input with TDesign's input class names (styled by
         tdesign-overrides.css): the search box is on most first screens and
         must not pull in TDesign's Input runtime. -->
    <div class="t-input__wrap">
      <div class="t-input t-input--prefix" :class="{ 't-input--suffix': !!query }">
        <span class="t-input__prefix t-input__prefix-icon" aria-hidden="true"><t-icon name="search" /></span>
        <input
          ref="inputControl"
          v-model="query"
          class="t-input__inner"
          type="search"
          name="q"
          autocomplete="off"
          :aria-label="$t('shared.SearchBox.s1')"
          :placeholder="placeholder || $t('shared.SearchBox.s2')"
        />
        <span v-if="query" class="t-input__suffix t-input__suffix-icon">
          <button type="button" class="search-box__clear" :aria-label="$t('shared.SearchBox.s3')" @click.stop="clearSearch">
            <t-icon name="close" :size="16" />
          </button>
        </span>
      </div>
    </div>
    <button type="submit" class="btn btn--primary btn--sm search-box__submit" :disabled="!query.trim() && !allowEmpty">{{ $t('shared.SearchBox.s1') }}</button>
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
  display: flex;
  align-items: center;
  gap: var(--space-2);
  box-sizing: border-box;
  height: var(--hit-target);
  border-radius: var(--radius-full);
  padding: 0 var(--space-4);
}

.search-box--has-query :deep(.t-input) {
  padding-inline-end: var(--space-1);
}

.search-box :deep(.t-input__prefix-icon),
.search-box :deep(.t-input__suffix-icon) {
  flex: 0 0 auto;
}

.search-box :deep(.t-input__suffix-icon) {
  margin-inline-start: var(--space-1);
}

/* Base layout formerly supplied by TDesign's input stylesheet. */
.search-box .t-input__inner {
  flex: 1;
  min-width: 0;
  height: 100%;
  padding: 0;
  border: none;
  outline: none;
  background: transparent;
  font: inherit;
}

.search-box .t-input__prefix,
.search-box .t-input__suffix {
  display: inline-flex;
  align-items: center;
}

/* Keep one accessible clear button instead of browser-specific search chrome. */
.search-box :deep(input[type='search']::-webkit-search-decoration),
.search-box :deep(input[type='search']::-webkit-search-cancel-button),
.search-box :deep(input[type='search']::-webkit-search-results-button),
.search-box :deep(input[type='search']::-webkit-search-results-decoration) {
  -webkit-appearance: none;
  appearance: none;
  display: none;
}

.search-box__clear {
  display: grid;
  place-items: center;
  flex: none;
  width: 2.25rem;
  height: 2.25rem;
  border-radius: var(--radius-full);
  color: var(--color-label-secondary);
}

.search-box__clear:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: -3px;
}

.search-box__clear:active {
  background: var(--color-pressed);
}

.search-box__submit {
  flex: 0 0 auto;
  min-height: var(--hit-target);
}

@media (hover: hover) and (pointer: fine) {
  .search-box__clear:hover { background: var(--color-hover); }
}

@media (pointer: coarse) {
  .search-box__clear { width: var(--hit-target); height: var(--hit-target); }
}
</style>
