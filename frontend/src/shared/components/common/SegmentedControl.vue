<script setup lang="ts" generic="T extends string">
import { computed } from 'vue'

/**
 * iOS-style segmented control. The selection "thumb" is one element that
 * slides between segments with a CSS transition on transform — so it is
 * interruptible: tapping again mid-slide retargets from where it is.
 */
const props = defineProps<{
  modelValue: T
  options: { value: T; label: string; icon?: string }[]
  label: string
}>()

const emit = defineEmits<{ 'update:modelValue': [value: T] }>()

const index = computed(() => Math.max(0, props.options.findIndex((o) => o.value === props.modelValue)))

function onKeydown(e: KeyboardEvent) {
  const dir = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0
  if (!dir) return
  e.preventDefault()
  const next = props.options[(index.value + dir + props.options.length) % props.options.length]
  if (next) {
    emit('update:modelValue', next.value)
    const root = (e.currentTarget as HTMLElement)
    requestAnimationFrame(() => root.querySelector<HTMLElement>('[aria-checked="true"]')?.focus())
  }
}
</script>

<template>
  <div
    class="segmented"
    role="radiogroup"
    :aria-label="label"
    :style="{ '--count': options.length, '--index': index }"
    @keydown="onKeydown"
  >
    <span class="segmented__thumb" aria-hidden="true" />
    <button
      v-for="opt in options"
      :key="opt.value"
      type="button"
      role="radio"
      class="segmented__item"
      :class="{ 'segmented__item--active': opt.value === modelValue }"
      :aria-checked="opt.value === modelValue"
      :tabindex="opt.value === modelValue ? 0 : -1"
      @click="emit('update:modelValue', opt.value)"
    >
      <t-icon v-if="opt.icon" :name="opt.icon" :size="16" />
      <span>{{ opt.label }}</span>
    </button>
  </div>
</template>

<style scoped>
.segmented {
  position: relative;
  display: grid;
  grid-template-columns: repeat(var(--count), 1fr);
  padding: 2px;
  border-radius: 9px;
  background: var(--color-fill);
  isolation: isolate;
}

.segmented__thumb {
  position: absolute;
  top: 2px;
  bottom: 2px;
  left: 2px;
  width: calc((100% - 4px) / var(--count));
  border-radius: 7px;
  background: var(--color-bg-row);
  box-shadow: 0 3px 8px rgba(0, 0, 0, 0.12), 0 0 0 0.5px rgba(0, 0, 0, 0.04);
  transform: translateX(calc(var(--index) * 100%));
  transition: transform 300ms var(--ease-spring);
  z-index: -1;
}

:root[theme-mode='dark'] .segmented__thumb {
  background: #636366;
  box-shadow: 0 3px 8px rgba(0, 0, 0, 0.3);
}

.segmented__item {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
  min-height: 2rem;
  padding: 0 var(--space-2);
  border-radius: 7px;
  color: var(--color-label);
  font-size: var(--text-subhead);
  font-weight: var(--weight-medium);
  -webkit-tap-highlight-color: transparent;
  transition:
    opacity var(--duration-fast) var(--ease-out),
    transform var(--duration-release) var(--ease-out);
}

.segmented__item--active {
  font-weight: var(--weight-semibold);
}

/* Press: the pressed (inactive) segment dims; response on pointer-down. */
.segmented__item:not(.segmented__item--active):active {
  opacity: 0.55;
  transition-duration: 0s;
}

.segmented__item:focus-visible {
  outline: 2px solid var(--color-focus-ring);
  outline-offset: 1px;
}

.segmented__item :deep(svg) {
  width: 1rem;
  height: 1rem;
  flex-shrink: 0;
}
</style>
