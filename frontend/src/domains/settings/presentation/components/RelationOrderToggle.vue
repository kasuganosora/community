<script setup lang="ts">
import type { RelationOrder } from '../../infrastructure/moderationApi'

defineProps<{ modelValue: RelationOrder; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: RelationOrder] }>()
</script>

<template>
  <div class="relation-order" role="group" :aria-label="$t('settings.Blocks.sort')">
    <button
      v-for="value in (['newest', 'oldest'] as const)"
      :key="value"
      type="button"
      class="relation-order__item"
      :aria-pressed="modelValue === value"
      :disabled="disabled"
      @click="emit('update:modelValue', value)"
    >
{{ value === 'newest' ? $t('settings.Blocks.newest') : $t('settings.Blocks.oldest') }}
</button>
  </div>
</template>

<style scoped>
.relation-order { display: inline-flex; gap: .25rem; padding: .1875rem; border-radius: 999px; background: var(--color-fill, var(--color-hover)); }
.relation-order__item { min-height: 32px; padding: .25rem .75rem; border-radius: 999px; color: var(--color-label-secondary); font-size: .8125rem; font-weight: 600; }
.relation-order__item[aria-pressed='true'] { background: var(--color-bg-row, var(--color-bg)); color: var(--color-label); box-shadow: 0 1px 2px rgb(0 0 0 / .12); }
.relation-order__item:disabled { opacity: .6; }
</style>
