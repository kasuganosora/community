<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { MastodonStatus } from '@/shared/types/mastodon'

const props = defineProps<{
  marks?: MastodonStatus['filtered']
}>()

const revealed = ref(false)
const level = computed(() => {
  const marks = (props.marks ?? []).map(mark => typeof mark === 'string' ? mark : mark?.filter?.filter_action)
  if (marks.includes('hide')) return 'hide'
  if (marks.includes('warn')) return 'warn'
  return ''
})

watch(() => props.marks, () => {
  revealed.value = false
})
</script>

<template>
  <div v-if="level && !revealed" class="filtered-notice" @click.stop>
    <p>{{ level === 'hide' ? '这条帖子已被隐藏' : '这条帖子可能不适合直接看' }}</p>
    <button type="button" @click="revealed = true">仍然查看</button>
  </div>
  <slot v-else />
</template>

<style scoped>
.filtered-notice {
  margin: var(--space-2) 0;
  padding: var(--space-3);
  border-radius: var(--radius-sm);
  background: var(--color-fill);
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  line-height: var(--leading-footnote);
}

.filtered-notice button {
  margin-top: var(--space-2);
  color: var(--color-accent-text);
  font-weight: var(--weight-semibold);
}
</style>
