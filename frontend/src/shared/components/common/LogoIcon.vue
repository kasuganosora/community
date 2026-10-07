<script setup lang="ts">
defineProps<{
  size?: number
}>()
</script>

<template>
  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" :width="size || 28" :height="size || 28" role="img" aria-label="Community Logo">
    <path fill="#009FE3" d="m17 42-1.6 12.1c-.2 1.6 1.4 2.7 2.8 1.9L31 48.5Z"/>
    <path fill="none" stroke="#009FE3" stroke-width="12" stroke-linecap="round" d="M49.3 20a20 20 0 1 0 0 20"/>
  </svg>
</template>
