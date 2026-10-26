<script setup lang="ts">
// Lives in the sticky page header so it never covers a post or shifts the feed.
import { onBeforeUnmount, ref, watch } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import { t } from '@/shared/i18n'

const props = defineProps<{
  show: boolean
  reveal: () => Promise<unknown> | unknown
}>()
const busy = ref(false)
const failed = ref(false)
const slow = ref(false)
let disposed = false
let timer: ReturnType<typeof setTimeout> | undefined
watch(() => props.show, show => { if (!show) failed.value = false })
onBeforeUnmount(() => { disposed = true; clearTimeout(timer) })

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
  <div class="new-posts-control">
    <span class="sr-only" role="status" aria-live="polite" aria-atomic="true">{{ slow ? $t('shared.NewPostsPill.s4') : show ? $t('shared.NewPostsPill.s5') : '' }}</span>
    <button
      v-if="show || busy"
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
