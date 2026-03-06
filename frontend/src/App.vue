<script setup lang="ts">
import { onMounted } from 'vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'

const authStore = useAuthStore()

onMounted(() => {
  authStore.initialize()
})
</script>

<template>
  <router-view />
</template>
