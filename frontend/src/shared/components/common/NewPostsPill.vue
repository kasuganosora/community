<script setup lang="ts">
// Either a header action or an in-flow row, never an overlay on post content.
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import { t } from '@/shared/i18n'

const props = defineProps<{
  show: boolean
  layout?: 'header' | 'row'
  reveal: () => Promise<unknown> | unknown
}>()
const busy = ref(false)
const failed = ref(false)
const slow = ref(false)
const control = ref<HTMLElement>()
// A row must collapse as the head is committed, before reveal positions the
// first post. Keeping it until the callback settles would shift that post again.
const visible = computed(() => props.show || (busy.value && props.layout !== 'row'))
let disposed = false
let timer: ReturnType<typeof setTimeout> | undefined
watch(() => props.show, show => { if (!show) failed.value = false })
onBeforeUnmount(() => { disposed = true; clearTimeout(timer) })
watch(visible, async () => {
  const element = control.value
  if (props.layout !== 'row' || !element || element.getBoundingClientRect().top >= 0) return
  const height = element.getBoundingClientRect().height
  const scroll = window.scrollY
  await nextTick()
  if (disposed || control.value !== element || window.scrollY !== scroll) return
  // When the row changes above the viewport, keep the post being read at its
  // current position. An explicit/user scroll already takes precedence.
  const delta = element.getBoundingClientRect().height - height
  if (delta) window.scrollTo({ top: Math.max(0, scroll + delta), behavior: 'instant' })
})

async function onClick() {
  if (busy.value) return
  failed.value = false
  try {
    const result = props.reveal()
    if (!result || typeof (result as Promise<unknown>).then !== 'function') return
    busy.value = true
    timer = setTimeout(() => { slow.value = true }, 6000)
    await result
  } catch (error) {
    if (disposed) return
    failed.value = true
    MessagePlugin.error(error instanceof Error ? error.message : t('shared.NewPostsPill.s3'))
  } finally {
    clearTimeout(timer)
    if (!disposed) { busy.value = false; slow.value = false }
  }
}
</script>

<template>
  <div ref="control" class="new-posts-control" :class="{ 'new-posts-control--row': layout === 'row' }">
    <span class="sr-only" role="status" aria-live="polite" aria-atomic="true">{{ slow ? $t('shared.NewPostsPill.s4') : show ? $t('shared.NewPostsPill.s5') : '' }}</span>
    <button
      v-if="visible"
      type="button"
      class="new-posts-pill"
      data-testid="new-posts-pill"
      :disabled="busy"
      :aria-busy="busy || undefined"
      :title="slow ? $t('shared.NewPostsPill.s4') : undefined"
      @click="onClick"
    >
      <t-loading v-if="busy" size="small" aria-hidden="true" />
      <t-icon v-else :name="failed ? 'refresh' : 'arrow-up'" :size="16" aria-hidden="true" />
      <span>{{ busy ? $t('shared.NewPostsPill.s2') : failed ? $t('shared.NewPostsPill.s3') : $t('shared.NewPostsPill.s1') }}</span>
    </button>
  </div>
</template>

<style scoped>
.new-posts-control { flex: none; margin-inline-start: auto; }
.new-posts-control--row { display: flex; margin: 0; padding: 0 var(--gutter); overflow-anchor: none; }
.new-posts-control--row .new-posts-pill {
  width: 100%;
  margin-bottom: var(--space-3);
  border-radius: var(--radius-md);
  background: var(--color-accent-soft);
  color: var(--color-accent-text);
}
.new-posts-pill {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
  min-height: var(--hit-target);
  padding: 0 var(--space-3);
  white-space: nowrap;
  border-radius: var(--radius-full);
  background: var(--color-accent);
  color: var(--color-on-accent, #fff);
  font-size: var(--text-footnote);
  font-weight: var(--weight-semibold);
  cursor: pointer;
}
.new-posts-pill:focus-visible { outline: 2px solid var(--color-focus-ring, var(--color-accent)); outline-offset: 2px; }
.new-posts-pill:disabled { cursor: progress; opacity: 1; }
.new-posts-pill :deep(.app-loading) { color: inherit; }
</style>
