<script setup lang="ts">
defineProps<{ label: string; hint?: string; disabled?: boolean }>()
const model = defineModel<boolean>({ default: false })
</script>

<template>
  <label class="admin-check" :class="{ 'admin-check--disabled': disabled }">
    <input v-model="model" type="checkbox" :disabled="disabled" />
    <span class="admin-check__text">{{ label }}<small v-if="hint">{{ hint }}</small></span>
  </label>
</template>
