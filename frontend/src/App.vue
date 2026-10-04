<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'

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
  <div v-if="initializing" class="app-initializing">
    <t-loading size="large" />
  </div>
  <div v-else-if="initError" class="app-init-error">
    <p>{{ initError }}</p>
    <t-button theme="primary" @click="retryInit">重试</t-button>
  </div>
  <router-view v-else />
</template>


<style scoped>
.app-initializing {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100dvh;
  background: var(--color-bg);
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
