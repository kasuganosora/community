<script setup lang="ts">
// Floating "new posts" pill: a zero-height sticky anchor so it never pushes
// the feed down, with the pill centred in the main column below the header.
import { ref } from 'vue'

const props = withDefaults(defineProps<{
  show: boolean
  top?: string
  // A prop, not an emit: Vue's emit() drops the listener's return value, so the
  // pill could not stay busy until the timeline refresh actually finished.
  reveal: () => Promise<unknown> | unknown
}>(), {
  top: 'calc(var(--bar-height) + var(--safe-top) + var(--space-3))',
})

const busy = ref(false)

function isPromise(value: unknown): value is Promise<unknown> {
  return !!value && typeof (value as Promise<unknown>).then === 'function'
}

async function onClick() {
  if (busy.value) return
  // A finished prefetch resolves synchronously. Awaiting it would still yield
  // and flash the spinner for a result that is already in memory.
  let result: unknown
  try {
    result = props.reveal()
  } catch {
    return
  }
  if (!isPromise(result)) return
  busy.value = true
  try {
    await result
  } catch {
    // The timeline already records the failure. The pill must accept another try.
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="new-posts-anchor" :style="{ top }">
    <Transition name="new-posts">
      <button
        v-if="show"
        type="button"
        class="new-posts-pill"
        data-testid="new-posts-pill"
        :disabled="busy"
        :aria-busy="busy || undefined"
        @click="onClick"
      >
        <t-loading v-if="busy" size="small" aria-hidden="true" />
        <t-icon v-else name="arrow-up" :size="16" aria-hidden="true" />
        <span>{{ busy ? $t('shared.NewPostsPill.s2') : $t('shared.NewPostsPill.s1') }}</span>
      </button>
    </Transition>
  </div>
</template>

<style scoped>
.new-posts-anchor {
  position: sticky;
  z-index: calc(var(--z-bar) - 1);
  height: 0;
  overflow: visible;
}
.new-posts-pill {
  position: absolute;
  top: var(--space-3);
  left: 50%;
  transform: translateX(-50%);
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-height: 2.25rem;
  padding: 0 var(--space-4);
  white-space: nowrap;
  border-radius: var(--radius-full);
  background: var(--color-accent);
  color: var(--color-on-accent, #fff);
  font-size: var(--text-footnote);
  font-weight: var(--weight-semibold);
  box-shadow: var(--shadow-2);
  cursor: pointer;
}
.new-posts-pill:focus-visible { outline: 2px solid var(--color-focus-ring, var(--color-accent)); outline-offset: 2px; }
/* The global disabled fade would wash the spinner out. Disabled only blocks a second click. */
.new-posts-pill:disabled { cursor: progress; opacity: 1; }
.new-posts-pill :deep(.app-loading) { color: inherit; }
.new-posts-enter-active, .new-posts-leave-active { transition: opacity .2s ease, transform .2s ease; }
.new-posts-enter-from, .new-posts-leave-to { opacity: 0; transform: translate(-50%, -8px); }
@media (prefers-reduced-motion: reduce) {
  .new-posts-enter-active, .new-posts-leave-active { transition: none; }
}
</style>
