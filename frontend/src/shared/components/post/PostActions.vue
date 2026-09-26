<script setup lang="ts">
import { computed } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
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

async function handleShare() {
  const url = props.status.url || props.status.uri || window.location.href
  if (navigator.share) {
    try {
      await navigator.share({ url })
    } catch (err) {
      if (!(err instanceof DOMException && err.name === 'AbortError')) {
        MessagePlugin.error('分享失败')
      }
    }
  } else {
    try {
      await navigator.clipboard.writeText(url)
      MessagePlugin.success('已复制链接')
    } catch {
      MessagePlugin.error('复制失败，请手动复制')
    }
  }
}
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
      @click.stop="handleShare"
      aria-label="分享"
    >
      <t-icon name="share" :size="18" />
    </button>
  </div>
</template>

<style scoped>
/* Quiet by default: secondary-grey glyphs, colour only when a state is on.
   Each control keeps a 44pt hit area while the glyph row stays compact;
   the negative inline margin lines the first glyph up with the text. */
.post-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  max-width: 26rem;
  margin: var(--space-1) calc(-1 * var(--space-3)) 0;
}

.action-btn {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-width: var(--hit-target);
  min-height: var(--hit-target);
  padding: 0 var(--space-3);
  border-radius: var(--radius-full);
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  font-weight: var(--weight-regular);
  font-variant-numeric: tabular-nums;
  letter-spacing: var(--tracking-footnote);
  touch-action: manipulation;
  -webkit-tap-highlight-color: transparent;
  transition:
    color var(--duration-fast) var(--ease-out),
    transform var(--duration-release) var(--ease-out);
}

/* Soft circular wash behind the glyph (not the whole pill). */
.action-btn::before {
  content: '';
  position: absolute;
  left: calc(var(--space-3) - 0.5rem);
  top: 50%;
  width: 2.125rem;
  height: 2.125rem;
  margin-top: -1.0625rem;
  border-radius: 50%;
  background: currentColor;
  opacity: 0;
  transition: opacity var(--duration-fast) var(--ease-out);
  pointer-events: none;
}

.action-btn :deep(svg) {
  position: relative;
  width: 1.125rem;
  height: 1.125rem;
  transition: transform var(--duration-release) var(--ease-out);
}

/* Press: the glyph dips immediately, releases with an ease-out. */
.action-btn:active :deep(svg) {
  transform: scale(var(--press-scale-small));
  transition-duration: var(--duration-press);
}

.action-btn:active::before {
  opacity: 0.12;
  transition-duration: 0s;
}

@media (hover: hover) and (pointer: fine) {
  .action-btn--reply:hover,
  .action-btn--share:hover,
  .action-btn--bookmark:hover {
    color: var(--color-accent-text);
  }

  .action-btn--reblog:hover {
    color: var(--color-repost);
  }

  .action-btn--fav:hover {
    color: var(--color-like);
  }

  .action-btn:hover::before {
    opacity: 0.1;
  }
}

.action-btn--active-green {
  color: var(--color-repost);
}

.action-btn--active-pink {
  color: var(--color-like);
}

.action-btn--active-blue {
  color: var(--color-accent-text);
}

.action-btn:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

.action-count {
  position: relative;
  min-width: 1ch;
}

.action-btn:focus-visible {
  outline: 2px solid var(--color-focus-ring);
  outline-offset: -2px;
}
</style>
