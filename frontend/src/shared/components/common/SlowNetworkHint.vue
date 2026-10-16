<script setup lang="ts">
import { stalledRequests } from '@/shared/api/stallRetry'
</script>
<template>
  <div v-if="stalledRequests > 0" class="slow-network-hint" role="status" aria-live="polite">{{ $t('shared.SlowNetworkHint.s1') }}</div>
</template>
<style scoped>
.slow-network-hint {
  position: fixed; left: 50%; top: calc(env(safe-area-inset-top, 0px) + 8px); transform: translateX(-50%);
  z-index: 3000; padding: 4px 12px; border-radius: 999px; font-size: 12px; pointer-events: none;
  color: var(--color-text-secondary); background: var(--color-bg-card); border: 1px solid var(--color-border);
  box-shadow: 0 2px 8px rgb(0 0 0 / 8%);
}
</style>
