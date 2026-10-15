<script setup lang="ts">
/**
 * Local button used for <t-button> (mapped in vite.config.ts). It renders
 * TDesign's class names, so the design-system overrides in
 * shared/styles/tdesign-overrides.css keep styling it, without loading
 * TDesign's Button runtime and its 40 KB stylesheet on every page.
 * Supports the props this app uses: theme, variant, size, shape, loading,
 * disabled, block, type and href.
 */
import { computed } from 'vue'
import AppLoading from './AppLoading.vue'

const props = withDefaults(defineProps<{
  theme?: 'default' | 'primary' | 'danger' | 'warning' | 'success'
  variant?: 'base' | 'outline' | 'dashed' | 'text'
  size?: 'small' | 'medium' | 'large'
  shape?: 'rectangle' | 'square' | 'round' | 'circle'
  type?: 'button' | 'submit' | 'reset'
  loading?: boolean
  disabled?: boolean
  block?: boolean
  href?: string
}>(), { theme: undefined, variant: 'base', size: 'medium', shape: 'rectangle', type: 'button', href: undefined })

const emit = defineEmits<{ click: [event: MouseEvent] }>()
const sizeClass = { small: 't-size-s', medium: 't-size-m', large: 't-size-l' } as const
const inactive = computed(() => props.disabled || props.loading)
const classes = computed(() => [
  't-button',
  `t-button--variant-${props.variant}`,
  `t-button--theme-${props.theme ?? (props.variant === 'base' ? 'primary' : 'default')}`,
  `t-button--shape-${props.shape}`,
  sizeClass[props.size],
  { 't-is-disabled': props.disabled, 't-is-loading': props.loading, 't-size-full-width': props.block },
])
function onClick(event: MouseEvent) {
  if (inactive.value) { event.preventDefault(); return }
  emit('click', event)
}
</script>

<template>
  <a v-if="href" :href="inactive ? undefined : href" :class="classes" :aria-disabled="inactive || undefined" @click="onClick">
    <AppLoading v-if="loading" size="small" class="app-button__spinner" />
    <span class="t-button__text"><slot /></span>
  </a>
  <button v-else :type="type" :class="classes" :disabled="inactive" :aria-busy="loading || undefined" @click="onClick">
    <AppLoading v-if="loading" size="small" class="app-button__spinner" />
    <span class="t-button__text"><slot /></span>
  </button>
</template>

<style>
/* Base box model formerly supplied by TDesign's button stylesheet; colours,
   sizes and radii come from tdesign-overrides.css. */
.t-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  box-sizing: border-box;
  max-width: 100%;
  margin: 0;
  border: 1px solid transparent;
  background: transparent;
  color: inherit;
  line-height: 1;
  white-space: nowrap;
  text-decoration: none;
  cursor: pointer;
  user-select: none;
  -webkit-tap-highlight-color: transparent;
  outline: none;
  vertical-align: middle;
}
.t-button:focus-visible { outline: 2px solid var(--color-focus-ring, var(--td-brand-color)); outline-offset: 2px; }
.t-button.t-is-disabled, .t-button:disabled { cursor: not-allowed; opacity: 0.6; }
.t-button.t-is-loading { cursor: progress; }
.t-button--variant-outline { border-color: var(--color-separator, currentColor); }
.t-button--shape-circle { border-radius: 50%; aspect-ratio: 1; }
.t-button--shape-square { aspect-ratio: 1; }
.t-button.t-size-full-width { display: flex; width: 100%; }
.t-button__text { display: inline-flex; align-items: center; gap: 4px; }
.app-button__spinner { color: inherit; }
</style>
