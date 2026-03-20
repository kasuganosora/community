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
  <router-view />
</template>
