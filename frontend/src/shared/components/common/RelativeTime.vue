<script setup lang="ts">
import { computed } from 'vue'
import { formatRelativeTime } from '@/shared/utils/format'

const props = defineProps<{
  datetime: string
}>()

const timeStr = computed(() => formatRelativeTime(props.datetime))
const fullDate = computed(() => new Date(props.datetime).toLocaleString())
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
