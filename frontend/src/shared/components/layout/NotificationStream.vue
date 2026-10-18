<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useFeedStream } from '@/shared/composables/useFeedStream'
import { useNavUnread, type NavUnreadKind } from '@/shared/composables/useNavUnread'
import { sessionEpoch } from '@/shared/api/sessionScope'

// Keeps the nav unread dots live. The notification stream with unread=1 also
// carries direct-message and announcement hints; every hint, reconnect and
// window focus refetches the one combined /api/v1/unread_counts. No polling.
const route = useRoute()
const { refreshNavUnread, setOpenPage, resetNavUnread } = useNavUnread()
const pages: Record<string, NavUnreadKind> = { '/notifications': 'notifications', '/announcements': 'announcements', '/messages': 'conversations' }
const pageFor = (path: string) => Object.entries(pages).find(([prefix]) => path === prefix || path.startsWith(`${prefix}/`))?.[1] ?? null
watch(() => route.path, path => setOpenPage(pageFor(path)), { immediate: true })
useFeedStream(ref('stream=notification&unread=1'), refreshNavUnread, { poll: false })
watch(sessionEpoch, () => { resetNavUnread(); void refreshNavUnread() })
</script>

<template><span hidden /></template>
