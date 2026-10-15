<script setup lang="ts">
import SlowNetworkHint from '@/shared/components/common/SlowNetworkHint.vue'
import { onMounted, ref } from 'vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { navigationPending } from '@/shared/router/navigationFeedback'

const authStore = useAuthStore()
const initializing = ref(true)
const initError = ref<string | null>(null)

function retryInit() {
  window.location.reload()
}

onMounted(async () => {
  try {
    await authStore.initialize()
  } catch (e) {
    console.error('Failed to initialize auth:', e)
    const msg = e instanceof Error ? e.message : String(e)
    initError.value = `应用启动失败：${msg}`
  } finally {
    initializing.value = false
  }
})
</script>


<template>
  <div v-if="navigationPending" class="navigation-pending" role="status" aria-label="正在打开页面"><span /></div>
  <div v-if="initializing" class="app-initializing">
    <t-loading size="large" />
  </div>
  <div v-else-if="initError" class="app-init-error">
    <p>{{ initError }}</p>
    <button type="button" class="app-init-error__retry" @click="retryInit">重试</button>
  </div>
  <router-view v-else />
  <SlowNetworkHint />
</template>


<style scoped>
.navigation-pending {
  position: fixed;
  inset: 0 0 auto;
  z-index: 10000;
  height: 2px;
  overflow: hidden;
  pointer-events: none;
}
.navigation-pending span {
  display: block;
  width: 40%;
  height: 100%;
  background: var(--color-accent);
  animation: navigation-progress 1s ease-in-out infinite;
}
@keyframes navigation-progress { from { transform: translateX(-100%); } to { transform: translateX(350%); } }
@media (prefers-reduced-motion: reduce) {
  .navigation-pending span { width: 100%; animation: none; }
}

.app-initializing {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100dvh;
  background: var(--color-bg);
}

/* Plain button: the app shell must not pull TDesign into the first screen. */
.app-init-error__retry {
  min-height: 36px;
  padding: 0 20px;
  border: none;
  border-radius: var(--radius-full, 999px);
  background: var(--color-accent, var(--td-brand-color));
  color: #fff;
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}
.app-init-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 100dvh;
  padding: var(--space-8) var(--gutter);
  gap: var(--space-4);
  color: var(--color-label-secondary);
  font-size: var(--text-body);
  text-align: center;
}
</style>
