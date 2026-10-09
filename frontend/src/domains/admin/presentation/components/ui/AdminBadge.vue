<script setup lang="ts">
withDefaults(defineProps<{ tone?: 'neutral' | 'success' | 'warning' | 'danger' | 'info' }>(), { tone: 'neutral' })
</script>

<template>
  <span class="admin-badge" :class="tone !== 'neutral' && `admin-badge--${tone}`"><slot /></span>
</template>
