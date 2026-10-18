<script setup lang="ts">
/**
 * One collapsible block on the 屏蔽 tab (hidden reblogs / mutes / blocks /
 * domain blocks), styled after Misskey's folders. The list loads the first
 * time the section is opened.
 */
import { ref } from 'vue'

const props = defineProps<{
  icon: string
  title: string
  count?: number | null
  testid?: string
  defaultOpen?: boolean
}>()

const emit = defineEmits<{ open: [] }>()
const open = ref(!!props.defaultOpen)
const opened = ref(open.value)

function onToggle(e: Event) {
  open.value = (e.target as HTMLDetailsElement).open
  if (open.value && !opened.value) opened.value = true
  if (open.value) emit('open')
}
</script>

<template>
  <details class="relation-section" :open="open" :data-testid="testid" @toggle="onToggle">
    <summary class="relation-section__summary">
      <t-icon :name="icon" :size="20" class="relation-section__icon" aria-hidden="true" />
      <span class="relation-section__title">{{ title }}</span>
      <span v-if="count != null" class="relation-section__count">{{ count }}</span>
      <t-icon name="chevron-down" :size="18" class="relation-section__chevron" aria-hidden="true" />
    </summary>
    <div v-if="opened" class="relation-section__body">
      <slot />
    </div>
  </details>
</template>

<style scoped>
.relation-section { background: var(--color-bg-grouped); border: 1px solid var(--color-separator); border-radius: .875rem; min-width: 0; }
.relation-section__summary { display: flex; align-items: center; gap: .75rem; min-height: 52px; padding: .75rem 1.25rem; cursor: pointer; list-style: none; color: var(--color-label); font-weight: 600; font-size: .9375rem; border-radius: inherit; user-select: none; }
.relation-section__summary::-webkit-details-marker { display: none; }
.relation-section__summary:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
.relation-section__icon { flex: none; color: var(--color-label-secondary); }
.relation-section__title { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.relation-section__count { flex: none; min-width: 1.5rem; padding: 0 .5rem; border-radius: 999px; background: var(--color-fill, var(--color-hover)); color: var(--color-label-secondary); font-size: .75rem; font-weight: 600; line-height: 1.5rem; text-align: center; }
.relation-section__chevron { flex: none; color: var(--color-label-secondary); transition: transform .2s ease; }
.relation-section[open] .relation-section__chevron { transform: rotate(180deg); }
.relation-section__body { border-top: 1px solid var(--color-separator); padding: .25rem 1.25rem 1rem; }
@media (hover: hover) and (pointer: fine) {
  .relation-section__summary:hover { background: var(--color-hover); }
  .relation-section[open] .relation-section__summary:hover { border-radius: .875rem .875rem 0 0; }
}
@media (max-width: 699px) {
  .relation-section__summary { padding: .75rem 1rem; }
  .relation-section__body { padding: .25rem 1rem 1rem; }
}
@media (prefers-reduced-motion: reduce) { .relation-section__chevron { transition: none; } }
</style>
