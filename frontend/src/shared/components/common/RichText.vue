<script setup lang="ts">
import { computed } from 'vue'
import { sanitizeHtml } from '@/shared/utils/sanitize'

const props = defineProps<{
  html: string
}>()

const safeHtml = computed(() => sanitizeHtml(props.html))
</script>

<template>
  <div class="rich-text" v-html="safeHtml" />
</template>

<style scoped>
.rich-text {
  word-wrap: break-word;
  overflow-wrap: break-word;
  line-height: 1.5;
}

.rich-text :deep(p) {
  margin-bottom: 0.5em;
}

.rich-text :deep(p:last-child) {
  margin-bottom: 0;
}

.rich-text :deep(a) {
  color: var(--accent-blue);
  text-decoration: none;
}

.rich-text :deep(a:hover) {
  text-decoration: underline;
}
</style>
