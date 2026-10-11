<script setup lang="ts">
import { useId } from 'vue'
defineProps<{ label: string; hint?: string; error?: string }>()
const id = useId()
</script>

<template>
  <div class="admin-field" :class="{ 'admin-field--error': error }">
    <label class="admin-field__label" :for="id">{{ label }}</label>
    <div class="admin-field__control"><slot :id="id" :described-by="hint || error ? `${id}-hint` : undefined" /></div>
    <p v-if="error" :id="`${id}-hint`" class="admin-field__error" role="alert">{{ error }}</p>
    <p v-else-if="hint" :id="`${id}-hint`" class="admin-field__hint">{{ hint }}</p>
  </div>
</template>
