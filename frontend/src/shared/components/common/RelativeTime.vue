<script setup lang="ts">
import { computed } from 'vue'
import { formatRelativeTime, formatTimeLeft } from '@/shared/utils/format'
import { useSharedClock } from '@/shared/composables/useSharedClock'

const props = defineProps<{
  datetime: string
  /** Show the time remaining until `datetime` instead of the time since. */
  future?: boolean
}>()

const now = useSharedClock()

const timeStr = computed(() => {
  // now.value is used as parameter, creating reactive dependency
  return props.future
    ? formatTimeLeft(props.datetime, now.value)
    : formatRelativeTime(props.datetime, now.value)
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
  color: var(--color-label-secondary);
  font-size: inherit;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
</style>
