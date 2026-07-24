<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { formatRelativeTime } from '@/shared/utils/format'

const props = defineProps<{
  datetime: string
}>()

const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  // Update relative time every 30 seconds
  timer = setInterval(() => {
    now.value = Date.now()
  }, 30000)
})

onUnmounted(() => {
  if (timer) {
    clearInterval(timer)
  }
})

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
