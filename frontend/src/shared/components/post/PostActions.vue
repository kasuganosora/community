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
    <button 
      class="action-btn action-btn--reply" 
      @click.stop="emit('reply')"
      :aria-label="replyCount ? `${replyCount} 条回复` : '回复'"
    >
      <t-icon name="chat" :size="18" />
      <span v-if="replyCount" class="action-count" aria-hidden="true">{{ replyCount }}</span>
    </button>

    <button
      class="action-btn action-btn--reblog"
      :class="{ 'action-btn--active-green': status.reblogged }"
      @click.stop="emit('reblog')"
      :aria-label="`${reblogCount || 0} 次转发. ${status.reblogged ? '取消转发' : '转发'}`"
      :aria-pressed="status.reblogged"
    >
      <t-icon name="swap" :size="18" />
      <span v-if="reblogCount" class="action-count" aria-hidden="true">{{ reblogCount }}</span>
    </button>

    <button
      class="action-btn action-btn--fav"
      :class="{ 'action-btn--active-pink': status.favourited }"
      @click.stop="emit('favourite')"
      :aria-label="`${favCount || 0} 个喜欢. ${status.favourited ? '取消喜欢' : '喜欢'}`"
      :aria-pressed="status.favourited"
    >
      <t-icon :name="status.favourited ? 'heart-filled' : 'heart'" :size="18" />
      <span v-if="favCount" class="action-count" aria-hidden="true">{{ favCount }}</span>
    </button>

    <button
      class="action-btn action-btn--bookmark"
      :class="{ 'action-btn--active-blue': status.bookmarked }"
      @click.stop="emit('bookmark')"
      :aria-label="status.bookmarked ? '移除收藏' : '收藏'"
      :aria-pressed="status.bookmarked"
    >
      <t-icon :name="status.bookmarked ? 'bookmark-filled' : 'bookmark'" :size="18" />
    </button>

    <button 
      class="action-btn action-btn--share" 
      @click.stop
      aria-label="分享"
      disabled
    >
      <t-icon name="share" :size="18" />
    </button>
  </div>
</template>

<style scoped>
.post-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: var(--space-3);
  max-width: 425px;
}

.action-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
  padding: var(--space-2);
  margin: calc(-1 * var(--space-2));
  border-radius: var(--radius-full);
  color: var(--text-secondary);
  transition: all var(--transition-fast);
  font-size: var(--font-sm);
  font-weight: var(--font-medium);
  min-width: 44px;
  min-height: 44px;
}

.action-btn:hover {
  color: var(--accent-blue);
  background: var(--accent-blue-light);
}

.action-btn:active {
  transform: scale(0.95);
}

.action-btn--reblog:hover {
  color: var(--accent-green);
  background: var(--accent-green-light);
}

.action-btn--fav:hover {
  color: var(--accent-pink);
  background: var(--accent-pink-light);
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

.action-btn:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}

.action-btn:disabled:hover {
  background: transparent;
  color: var(--text-secondary);
}

.action-count {
  font-size: var(--font-sm);
  font-weight: var(--font-medium);
  min-width: 1em;
  letter-spacing: var(--tracking-tight);
}

/* Focus styles */
.action-btn:focus-visible {
  outline: 2px solid var(--accent-blue);
  outline-offset: 2px;
}

.action-btn--active-green:focus-visible {
  outline-color: var(--accent-green);
}

.action-btn--active-pink:focus-visible {
  outline-color: var(--accent-pink);
}
</style>
