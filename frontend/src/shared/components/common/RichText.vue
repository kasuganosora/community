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
  overflow-wrap: anywhere;
  line-height: inherit;
}

.rich-text :deep(p) {
  margin: 0 0 0.5em;
}

.rich-text :deep(p:last-child) {
  margin-bottom: 0;
}

/* Links, mentions and hashtags share one quiet tint: the accent is the only
   colour in body text, and weight stays regular so it doesn't shout. */
.rich-text :deep(a) {
  color: var(--color-accent-text);
  text-decoration: none;
  border-radius: 3px;
  -webkit-tap-highlight-color: transparent;
}

.rich-text :deep(a:active) {
  background: var(--color-accent-soft);
}

@media (hover: hover) and (pointer: fine) {
  .rich-text :deep(a:hover) {
    text-decoration: underline;
    text-decoration-thickness: 1px;
    text-underline-offset: 2px;
  }
}

/* Long bare URLs wrap instead of pushing the column wider. */
.rich-text :deep(a:not(.mention)) {
  word-break: break-all;
}

.rich-text :deep(code) {
  font-size: 0.875em;
  padding: 0.1em 0.35em;
  border-radius: var(--radius-xs);
  background: var(--color-fill);
}
</style>
