<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useFeedStream } from '@/shared/composables/useFeedStream'
import { useNotificationUnread } from '@/shared/composables/useNotificationUnread'
import { sessionEpoch } from '@/shared/api/sessionScope'

// Keeps the notification badge live (SSE stream=notification, with the
// stream's polling fallback). The notifications page clears it itself.
const route = useRoute()
const { refreshUnread, resetUnread } = useNotificationUnread()
useFeedStream(ref('stream=notification'), () => route.path === '/notifications' ? Promise.resolve(true) : refreshUnread())
watch(sessionEpoch, () => { resetUnread(); void refreshUnread() })
</script>

<template><span hidden /></template>
