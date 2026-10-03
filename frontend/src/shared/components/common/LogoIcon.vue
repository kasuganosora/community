<script setup lang="ts">
defineProps<{
  size?: number
}>()
</script>

<template>
  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" :width="size || 28" :height="size || 28" role="img" aria-label="Community Logo">
    <path fill="#00B5F2" d="M36 6.5c12.8 0 22.2 10 22.2 22.3S48.8 51.2 36 51.2c-2.5 0-4.9-.4-7.1-1.1L11.2 58.4l5.8-11.8A21.5 21.5 0 0 1 13.8 28.8C13.8 16.5 23.2 6.5 36 6.5z"/>
    <circle cx="33.2" cy="29" r="4.1" fill="#fff"/>
    <circle cx="44.6" cy="29" r="4.1" fill="#fff"/>
  </svg>
</template>
