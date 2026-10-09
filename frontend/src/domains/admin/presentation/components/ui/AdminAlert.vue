<script setup lang="ts">
withDefaults(defineProps<{ tone?: 'success' | 'danger' | 'info' | 'warning' }>(), { tone: 'info' })
</script>

<template>
  <div class="admin-alert" :class="`admin-alert--${tone}`" :role="tone === 'danger' ? 'alert' : 'status'">
    <span class="admin-alert__text"><slot /></span>
    <span v-if="$slots.action" class="admin-alert__action"><slot name="action" /></span>
  </div>
</template>
