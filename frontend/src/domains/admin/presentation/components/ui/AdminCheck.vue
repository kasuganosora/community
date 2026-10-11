<script setup lang="ts">
defineProps<{ label: string; hint?: string; disabled?: boolean; variant?: 'checkbox' | 'switch' }>()
const model = defineModel<boolean>({ default: false })
</script>

<template>
  <label class="admin-check" :class="{ 'admin-check--disabled': disabled, 'admin-check--switch': variant === 'switch' }">
    <input v-model="model" :role="variant === 'switch' ? 'switch' : undefined" type="checkbox" :disabled="disabled" />
    <span class="admin-check__text">{{ label }}<small v-if="hint">{{ hint }}</small></span>
  </label>
</template>
