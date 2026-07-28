<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'

const authStore = useAuthStore()
const initializing = ref(true)
const initError = ref<string | null>(null)

onMounted(async () => {
  try {
    await authStore.initialize()
  } catch (e) {
    console.error('Failed to initialize auth:', e)
    initError.value = 'Failed to initialize application'
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
    <t-button theme="primary" @click="() => window.location.reload()">Retry</t-button>
  </div>
  <router-view v-else />
</template>


<style scoped>
.app-initializing {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
}

.app-init-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  gap: var(--space-4, 1rem);
  color: var(--text-secondary, #666);
}
</style>
