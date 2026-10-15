<script setup lang="ts">
/**
 * Small local spinner used for <t-loading> (mapped in vite.config.ts).
 * TDesign's Loading pulled ~65 KB of shared TDesign runtime into the first
 * screen; this keeps the same class names so existing styles still apply.
 */
withDefaults(defineProps<{ size?: 'small' | 'medium' | 'large' | string, text?: string }>(), { size: 'medium', text: '' })
</script>

<template>
  <span class="t-loading app-loading" :class="`app-loading--${size}`" role="status" aria-live="polite">
    <svg class="app-loading__svg" viewBox="0 0 24 24" aria-hidden="true">
      <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-dasharray="42 100" />
    </svg>
    <span v-if="text" class="t-loading__text">{{ text }}</span>
    <span v-else class="app-loading__sr">加载中</span>
  </span>
</template>

<style scoped>
.app-loading { display: inline-flex; align-items: center; justify-content: center; gap: 8px; vertical-align: middle; }
.app-loading__svg { width: 20px; height: 20px; animation: app-loading-spin 0.8s linear infinite; }
.app-loading--small .app-loading__svg { width: 16px; height: 16px; }
.app-loading--large .app-loading__svg { width: 28px; height: 28px; }
.app-loading__sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
@keyframes app-loading-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .app-loading__svg { animation-duration: 2.4s; } }
</style>
