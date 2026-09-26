<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { sanitizeHtml } from '@/shared/utils/sanitize'
import { internalRouteForLink } from '@/shared/utils/links'
import { localHosts } from '@/shared/utils/env'

const props = defineProps<{
  html: string
}>()

const safeHtml = computed(() => sanitizeHtml(props.html))
const router = useRouter()

// Mentions and hashtags open the in-app profile / tag page instead of a new
// tab on the server URL; other links keep opening externally.
function onClick(e: MouseEvent) {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
  const a = (e.target as Element | null)?.closest?.('a')
  if (!a) return
  const to = internalRouteForLink(a.getAttribute('href') ?? '', a.className, localHosts())
  if (!to) return
  e.preventDefault()
  e.stopPropagation()
  router.push(to).catch(() => {})
}
</script>

<template>
  <div class="rich-text" v-html="safeHtml" @click="onClick" />
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
