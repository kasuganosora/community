<script setup lang="ts">
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import NotificationStream from './NotificationStream.vue'

const authStore = useAuthStore()
</script>

<template>
  <NotificationStream v-if="authStore.currentUser" />
</template>
