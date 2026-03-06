<script setup lang="ts">
import { computed } from 'vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { formatCount } from '@/shared/utils/format'

const props = defineProps<{
  status: MastodonStatus
}>()

const emit = defineEmits<{
  reply: []
  reblog: []
  favourite: []
  bookmark: []
}>()

const replyCount = computed(() => props.status.replies_count > 0 ? formatCount(props.status.replies_count) : '')
const reblogCount = computed(() => props.status.reblogs_count > 0 ? formatCount(props.status.reblogs_count) : '')
const favCount = computed(() => props.status.favourites_count > 0 ? formatCount(props.status.favourites_count) : '')
</script>

<template>
  <div class="post-actions">
    <button class="action-btn action-btn--reply" @click.stop="emit('reply')">
      <t-icon name="chat" :size="18" />
      <span v-if="replyCount" class="action-count">{{ replyCount }}</span>
    </button>

    <button
      class="action-btn action-btn--reblog"
      :class="{ 'action-btn--active-green': status.reblogged }"
      @click.stop="emit('reblog')"
    >
      <t-icon name="swap" :size="18" />
      <span v-if="reblogCount" class="action-count">{{ reblogCount }}</span>
    </button>

    <button
      class="action-btn action-btn--fav"
      :class="{ 'action-btn--active-pink': status.favourited }"
      @click.stop="emit('favourite')"
    >
      <t-icon :name="status.favourited ? 'heart-filled' : 'heart'" :size="18" />
      <span v-if="favCount" class="action-count">{{ favCount }}</span>
    </button>

    <button
      class="action-btn action-btn--bookmark"
      :class="{ 'action-btn--active-blue': status.bookmarked }"
      @click.stop="emit('bookmark')"
    >
      <t-icon :name="status.bookmarked ? 'bookmark' : 'bookmark'" :size="18" />
    </button>

    <button class="action-btn action-btn--share" @click.stop>
      <t-icon name="share" :size="18" />
    </button>
  </div>
</template>

<style scoped>
.post-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 12px;
  max-width: 425px;
}

.action-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 8px;
  margin: -8px;
  border-radius: var(--radius-full);
  color: var(--text-secondary);
  transition: all var(--transition-fast);
  font-size: var(--font-xs);
}

.action-btn:hover {
  color: var(--accent-blue);
  background: rgba(29, 155, 240, 0.1);
}

.action-btn--reblog:hover {
  color: var(--accent-green);
  background: rgba(0, 186, 124, 0.1);
}

.action-btn--fav:hover {
  color: var(--accent-pink);
  background: rgba(249, 24, 128, 0.1);
}

.action-btn--active-green {
  color: var(--accent-green);
}

.action-btn--active-pink {
  color: var(--accent-pink);
}

.action-btn--active-blue {
  color: var(--accent-blue);
}

.action-count {
  font-size: var(--font-xs);
  min-width: 1em;
}
</style>
