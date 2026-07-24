<script setup lang="ts">
import { computed } from 'vue'
import { formatRelativeTime } from '@/shared/utils/format'
import { useSharedClock } from '@/shared/composables/useSharedClock'

const props = defineProps<{
  datetime: string
}>()

const now = useSharedClock()

const timeStr = computed(() => {
  // Reference now.value to create reactive dependency
  now.value
  return formatRelativeTime(props.datetime, now.value)
})
const fullDate = computed(() => {
  const d = new Date(props.datetime)
  return isNaN(d.getTime()) ? '' : d.toLocaleString()
})
</script>

<template>
  <time :datetime="datetime" :title="fullDate" class="relative-time">{{ timeStr }}</time>
</template>

<style scoped>
.relative-time {
  color: var(--text-secondary);
  font-size: var(--font-md);
  white-space: nowrap;
}
</style>
