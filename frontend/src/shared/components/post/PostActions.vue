<script setup lang="ts">
import { computed } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { canBoost, canQuote, canShare } from '@/domains/post/domain/compose'
import { formatCount } from '@/shared/utils/format'

const props = defineProps<{
  status: MastodonStatus
}>()

const emit = defineEmits<{
  reply: []
  reblog: []
  favourite: []
  bookmark: []
  quote: []
}>()

const showBoost = computed(() => canBoost(props.status.visibility))
const showQuote = computed(() => canQuote(props.status.visibility))
const showShare = computed(() => canShare(props.status.visibility))
const replyCount = computed(() => props.status.replies_count > 0 ? formatCount(props.status.replies_count) : '')
const reblogCount = computed(() => props.status.reblogs_count > 0 ? formatCount(props.status.reblogs_count) : '')
const favCount = computed(() => props.status.favourites_count > 0 ? formatCount(props.status.favourites_count) : '')

function shareUrl(status: MastodonStatus): string {
  const raw = status.url || status.uri || ''
  try {
    if (raw) return new URL(raw, window.location.origin).href
  } catch {
    // Fall through to the local permalink.
  }
  const name = status.account?.acct || status.account?.username || 'user'
  return new URL(`/@${name}/${status.id}`, window.location.origin).href
}

function copyText(text: string): boolean {
  const el = document.createElement('textarea')
  el.value = text
  el.setAttribute('readonly', '')
  el.style.position = 'fixed'
  el.style.top = '0'
  el.style.left = '0'
  el.style.opacity = '0'
  document.body.appendChild(el)
  el.focus()
  el.select()
  let ok: boolean
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  el.remove()
  return ok
}

async function handleShare() {
  const url = shareUrl(props.status)
  // Desktop browsers often expose share() but reject it outside a top-level
  // mobile sheet. Copy the link there instead of showing a dead error.
  const touch = window.matchMedia('(pointer: coarse)').matches
  if (touch && typeof navigator.share === 'function') {
    try {
      await navigator.share({ url })
      return
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') return
    }
  }
  if (copyText(url)) {
    MessagePlugin.success('已复制链接')
    return
  }
  try {
    await navigator.clipboard.writeText(url)
    MessagePlugin.success('已复制链接')
  } catch {
    MessagePlugin.error('分享失败')
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
      v-if="showBoost"
      class="action-btn action-btn--reblog"
      :class="{ 'action-btn--active-green': status.reblogged }"
      @click.stop="emit('reblog')"
      :aria-label="`${reblogCount || 0} 次转发. ${status.reblogged ? '取消转发' : '转发'}`"
      :aria-pressed="status.reblogged"
    >
      <Transition name="glyph">
        <t-icon :key="status.reblogged ? 'on' : 'off'" name="swap" :size="18" />
      </Transition>
      <span v-if="reblogCount" class="action-count" aria-hidden="true">{{ reblogCount }}</span>
    </button>

    <button
      class="action-btn action-btn--fav"
      :class="{ 'action-btn--active-pink': status.favourited }"
      @click.stop="emit('favourite')"
      :aria-label="`${favCount || 0} 个喜欢. ${status.favourited ? '取消喜欢' : '喜欢'}`"
      :aria-pressed="status.favourited"
    >
      <Transition name="glyph">
        <t-icon :key="status.favourited ? 'on' : 'off'" :name="status.favourited ? 'heart-filled' : 'heart'" :size="18" />
      </Transition>
      <span v-if="favCount" class="action-count" aria-hidden="true">{{ favCount }}</span>
    </button>

    <button
      class="action-btn action-btn--bookmark"
      :class="{ 'action-btn--active-blue': status.bookmarked }"
      @click.stop="emit('bookmark')"
      :aria-label="status.bookmarked ? '移除收藏' : '收藏'"
      :aria-pressed="status.bookmarked"
    >
      <Transition name="glyph">
        <t-icon :key="status.bookmarked ? 'on' : 'off'" :name="status.bookmarked ? 'bookmark-filled' : 'bookmark'" :size="18" />
      </Transition>
    </button>

    <button
      v-if="showQuote"
      class="action-btn action-btn--reply"
      @click.stop="emit('quote')"
      aria-label="引用"
    >
      <t-icon name="quote" :size="18" />
    </button>

    <button
      v-if="showShare"
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

/* State change on a control people hit many times a day: a short,
   interruptible settle, only when turning a state on. The old glyph leaves
   instantly so the two never overlap. */
.glyph-leave-active {
  position: absolute;
  opacity: 0;
}

.action-btn--active-pink .glyph-enter-active,
.action-btn--active-green .glyph-enter-active,
.action-btn--active-blue .glyph-enter-active {
  transition: transform 140ms var(--ease-out), opacity 140ms var(--ease-out);
}

.action-btn--active-pink .glyph-enter-from,
.action-btn--active-green .glyph-enter-from,
.action-btn--active-blue .glyph-enter-from {
  transform: scale(0.92);
  opacity: 0.7;
}

@media (prefers-reduced-motion: reduce) {
  .glyph-enter-from { transform: none; opacity: 1; }
}

.action-count {
  position: relative;
  min-width: 1ch;
}

.action-btn:focus-visible {
  outline: 2px solid var(--color-focus-ring);
  outline-offset: -2px;
}
@media (max-width: 360px) {
  .post-actions { flex-wrap: wrap; }
  .action-btn { padding-inline: 8px; gap: 2px; }
  .action-btn::before { left: 0; }
}
</style>
