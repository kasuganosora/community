<script setup lang="ts">
/**
 * Read-only status for the logged-out landing page: author, time, content
 * warning, rich text with custom emoji, media thumbnails and a quote/link hint.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { MastodonAttachment, MastodonStatus } from '@/shared/types/mastodon'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import RichText from '@/shared/components/common/RichText.vue'
import RelativeTime from '@/shared/components/common/RelativeTime.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { sameOriginMediaUrl } from '@/shared/utils/mediaUrl'

const props = withDefaults(defineProps<{
  status: MastodonStatus
  /** Lines shown before "show more". */
  lines?: number
  avatarSize?: number
}>(), {
  lines: 6,
  avatarSize: 36,
})

const shown = computed(() => props.status.reblog?.id ? props.status.reblog : props.status)
const booster = computed(() => props.status.reblog?.id ? props.status.account : null)
const hasCw = computed(() => !!shown.value.spoiler_text)
const cwOpen = ref(false)
const mediaRevealed = ref(false)
const expanded = ref(false)
const overflowing = ref(false)
const textEl = ref<HTMLElement>()

const media = computed(() => shown.value.media_attachments ?? [])
const thumbs = computed(() => media.value.filter(m => ['image', 'video', 'gifv'].includes(m.type) && thumbUrl(m)).slice(0, 4))
const otherMedia = computed(() => media.value.length - thumbs.value.length)
const mediaBlurred = computed(() => shown.value.sensitive && !mediaRevealed.value)
const quoteUrl = computed(() => safeHttp(shown.value.quote?.url || shown.value.quote?.uri))
const cardUrl = computed(() => safeHttp(shown.value.card?.url))
const pollCount = computed(() => shown.value.poll?.options.length ?? 0)

const hiddenSummary = computed(() => {
  const bits: string[] = []
  if (media.value.length) bits.push(`${media.value.length} 个媒体`)
  if (shown.value.poll) bits.push('投票')
  return bits.join(' · ')
})

function thumbUrl(m: MastodonAttachment): string {
  return sameOriginMediaUrl(m.preview_url || (m.type === 'image' ? m.url : ''))
}

function safeHttp(raw: string | null | undefined): string | null {
  if (!raw) return null
  try {
    const url = new URL(raw, window.location.origin)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) return null
    return url.href
  } catch {
    return null
  }
}

function hostOf(raw: string): string {
  try { return new URL(raw).host } catch { return raw }
}

async function measure() {
  await nextTick()
  const el = textEl.value
  if (!el || expanded.value) return
  overflowing.value = el.scrollHeight > el.clientHeight + 2
}

watch(() => shown.value.id, () => {
  cwOpen.value = false
  mediaRevealed.value = false
  expanded.value = false
  void measure()
})
watch(cwOpen, open => { if (open) void measure() })

// Width changes (column resize, web fonts, emoji images) change the line
// count, so re-check whether the text is clamped.
let resizeObserver: ResizeObserver | null = null
watch(textEl, (el, old) => {
  if (old) resizeObserver?.unobserve(old)
  if (el) resizeObserver?.observe(el)
})
onMounted(() => {
  if (typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(() => { void measure() })
    if (textEl.value) resizeObserver.observe(textEl.value)
  }
  void measure()
})
onBeforeUnmount(() => resizeObserver?.disconnect())
</script>

<template>
  <article class="preview-status">
    <p v-if="booster" class="preview-status__boost">
      <t-icon name="swap" :size="12" />
      <EmojiText :text="booster.display_name || booster.username" :emojis="booster.emojis" /> 转发了
    </p>
    <header class="preview-status__head">
      <UserAvatar :account="shown.account" :size="avatarSize" />
      <div class="preview-status__who">
        <span class="preview-status__name" dir="auto"><EmojiText :text="shown.account.display_name || shown.account.username" :emojis="shown.account.emojis" /></span>
        <span class="preview-status__acct">@{{ shown.account.acct }}</span>
      </div>
      <RelativeTime class="preview-status__time" :datetime="shown.created_at" />
    </header>

    <div v-if="hasCw" class="preview-status__cw">
      <span class="preview-status__cw-text" dir="auto"><EmojiText :text="shown.spoiler_text" :emojis="shown.emojis" /></span>
      <span v-if="!cwOpen && hiddenSummary" class="preview-status__meta">{{ hiddenSummary }}</span>
      <button type="button" class="preview-status__toggle" :aria-expanded="cwOpen" @click="cwOpen = !cwOpen">
        {{ cwOpen ? '收起' : '显示内容' }}
      </button>
    </div>

    <div v-if="!hasCw || cwOpen" class="preview-status__body">
      <div
        v-if="shown.content"
        ref="textEl"
        class="preview-status__text"
        :class="{ 'is-clamped': !expanded }"
        :style="{ '-webkit-line-clamp': lines }"
      >
        <RichText :html="shown.content" :emojis="shown.emojis" />
      </div>
      <button v-if="overflowing" type="button" class="preview-status__more" @click="expanded = !expanded">
        {{ expanded ? '收起' : '展开全文' }}
      </button>

      <div v-if="media.length" class="preview-status__media" :class="{ 'is-blurred': mediaBlurred }">
        <div v-if="thumbs.length" class="preview-status__thumbs" :data-count="thumbs.length">
          <span v-for="m in thumbs" :key="m.id" class="preview-status__thumb">
            <img :src="thumbUrl(m)" :alt="m.description || ''" loading="lazy" decoding="async" referrerpolicy="no-referrer" />
            <span v-if="m.type !== 'image'" class="preview-status__play" aria-hidden="true">▶</span>
          </span>
        </div>
        <span v-if="otherMedia > 0 || !thumbs.length" class="preview-status__meta">
          <t-icon name="file" :size="12" /> {{ media.length }} 个媒体
        </span>
        <button v-if="mediaBlurred" type="button" class="preview-status__reveal" @click="mediaRevealed = true">
          敏感内容，点击查看
        </button>
      </div>

      <p v-if="pollCount" class="preview-status__meta">
        <t-icon name="chart-bar" :size="12" /> 投票 · {{ pollCount }} 个选项
      </p>
      <a v-if="quoteUrl" class="preview-status__link" :href="quoteUrl" target="_blank" rel="noopener noreferrer nofollow">
        引用 · {{ hostOf(quoteUrl) }}
      </a>
      <a v-else-if="cardUrl" class="preview-status__link" :href="cardUrl" target="_blank" rel="noopener noreferrer nofollow">
        {{ shown.card?.title || hostOf(cardUrl) }}
      </a>
    </div>

    <footer class="preview-status__counts">
      <span><t-icon name="chat" :size="14" /> {{ shown.replies_count || '' }}</span>
      <span><t-icon name="swap" :size="14" /> {{ shown.reblogs_count || '' }}</span>
      <span><t-icon name="heart" :size="14" /> {{ shown.favourites_count || '' }}</span>
    </footer>
  </article>
</template>

<style scoped>
.preview-status {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.preview-status__boost {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--color-label-secondary);
  font-size: var(--text-caption);
}

.preview-status__head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.preview-status__who {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}

.preview-status__name,
.preview-status__acct {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.preview-status__name {
  font-weight: var(--weight-semibold);
  font-size: var(--text-subhead);
  color: var(--color-label);
}

.preview-status__acct,
.preview-status__time,
.preview-status__meta {
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
}

.preview-status__time {
  flex-shrink: 0;
  align-self: flex-start;
  font-variant-numeric: tabular-nums;
}

.preview-status__cw {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-sm);
  background: var(--color-fill);
}

.preview-status__cw-text {
  flex: 1;
  min-width: 0;
  font-weight: var(--weight-medium);
  color: var(--color-label);
  overflow-wrap: anywhere;
}

.preview-status__toggle,
.preview-status__more {
  color: var(--color-accent-text);
  font-size: var(--text-footnote);
  font-weight: var(--weight-semibold);
}

.preview-status__toggle {
  min-height: 1.75rem;
  padding: 0 var(--space-3);
  border-radius: var(--radius-full);
  background: var(--color-bg-row);
  box-shadow: var(--shadow-1);
}

.preview-status__more {
  align-self: flex-start;
}

.preview-status__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.preview-status__text {
  font-size: var(--text-subhead);
  line-height: var(--leading-subhead);
  color: var(--color-label);
  overflow-wrap: anywhere;
}

.preview-status__text.is-clamped {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.preview-status__media {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.preview-status__thumbs {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-1);
}

.preview-status__thumbs[data-count="1"] { grid-template-columns: minmax(0, 1fr); }
.preview-status__thumbs[data-count="2"] { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.preview-status__thumbs[data-count="3"] { grid-template-columns: repeat(3, minmax(0, 1fr)); }

.preview-status__thumb {
  position: relative;
  aspect-ratio: 16 / 9;
  overflow: hidden;
  border-radius: var(--radius-sm);
  background: var(--color-fill);
}

.preview-status__thumbs:not([data-count="1"]) .preview-status__thumb {
  aspect-ratio: 1;
}

.preview-status__thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.preview-status__play {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: #fff;
  font-size: 1.25rem;
  text-shadow: 0 1px 4px rgb(0 0 0 / 50%);
}

.preview-status__media.is-blurred .preview-status__thumbs {
  filter: blur(18px);
  pointer-events: none;
}

.preview-status__reveal {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  border-radius: var(--radius-sm);
  background: rgb(0 0 0 / 25%);
  color: #fff;
  font-size: var(--text-footnote);
  font-weight: var(--weight-semibold);
}

.preview-status__link {
  align-self: flex-start;
  max-width: 100%;
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--color-fill);
  color: var(--color-accent-text);
  font-size: var(--text-footnote);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-decoration: none;
}

.preview-status__counts {
  display: flex;
  gap: var(--space-5);
  color: var(--color-label-secondary);
  font-size: var(--text-caption);
  font-variant-numeric: tabular-nums;
}

.preview-status__counts span {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}
</style>
