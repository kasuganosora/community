<script setup lang="ts">
import { displayAccount } from '@/shared/utils/accountDisplay'
/**
 * Body of a status: content warning, text, media and poll. Shared by the
 * timeline card and the status detail page.
 */
import { ref, computed, watch, onMounted, nextTick } from 'vue'
import { useTimelineRowFlag } from '@/shared/composables/useTimelineRow'
import { useRouter } from 'vue-router'
import type { MastodonPoll, MastodonStatus } from '@/shared/types/mastodon'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import RichText from '@/shared/components/common/RichText.vue'
import PostMedia from './PostMedia.vue'
import { mediaGated, useMediaMode } from '@/shared/composables/useMediaMode'
import PostPoll from './PostPoll.vue'
import LinkPreviewCard from './LinkPreviewCard.vue'
import { useLinkPreview } from '@/shared/composables/useLinkPreview'
import { stripHtml } from '@/shared/utils/sanitize'
import { markBidiNumbers } from '@/shared/utils/bidi'

const props = defineProps<{
  status: MastodonStatus
  textClass?: string
}>()

const router = useRouter()
const cwOpen = useTimelineRowFlag('cwOpen')
const mediaRevealed = useTimelineRowFlag('mediaRevealed')
const pollOverride = ref<MastodonPoll | null>(null)
const expanded = useTimelineRowFlag('expanded')
const long = ref(false)
const textEl = ref<HTMLElement>()
const contentEl = ref<HTMLElement>()

watch(() => props.status.id, () => {
  cwOpen.value = false
  mediaRevealed.value = false
  pollOverride.value = null
  expanded.value = false
  void measure()
})
watch(() => props.status.content, () => { void measure() })

const ARABIC_SCRIPT = /^(ar|fa|ur|ps|sd|ug|ckb)(?:-|$)/i
function textDir(language?: string | null): 'rtl' | 'auto' {
  return language && ARABIC_SCRIPT.test(language) ? 'rtl' : 'auto'
}
const hasCw = computed(() => !!props.status.spoiler_text)
const bodyVisible = computed(() => !hasCw.value || cwOpen.value)
const poll = computed(() => pollOverride.value ?? props.status.poll)
// Media marked sensitive without a CW still needs a click to reveal.
const { mode: mediaMode } = useMediaMode()
// Gated media is shown as a BlurHash placeholder and is not requested at all
// until the viewer clicks. A CW already hides the whole body behind its own
// button, but the files still wait for their own click.
const gated = computed(() => mediaGated(props.status, mediaMode.value))
const mediaHidden = computed(() => gated.value && !mediaRevealed.value)
const contentId = ref(`status-content-${props.status.id}-${Math.random().toString(36).slice(2, 6)}`)
const quoted = computed(() => props.status.quote?.id ? props.status.quote : null)
const quoteText = computed(() => {
  const status = quoted.value
  if (!status) return ''
  if (status.spoiler_text) return markBidiNumbers(`内容警告：${status.spoiler_text}`)
  return markBidiNumbers(stripHtml(status.content || '').replace(/\s+/g, ' ').trim())
})
const hiddenSummary = computed(() => {
  const bits: string[] = []
  const media = props.status.media_attachments?.length ?? 0
  if (media) bits.push(`${media} 个媒体`)
  if (props.status.poll) bits.push('投票')
  if (quoted.value) bits.push('引用')
  else if (props.status.card?.url) bits.push('链接预览')
  return bits.join(' · ')
})

const fetchedCard = useLinkPreview(() => props.status, contentEl, () => bodyVisible.value)
const previewCard = computed(() => props.status.card ?? fetchedCard.value)

async function measure() {
  await nextTick()
  const el = textEl.value
  if (!el) return
  const clamped = el.classList.contains('is-clamped')
  if (clamped) el.classList.remove('is-clamped')
  long.value = el.scrollHeight > 220
  if (clamped && !expanded.value) el.classList.add('is-clamped')
}

function openQuote() {
  const status = quoted.value
  if (!status) return
  router.push({
    name: 'post-detail',
    params: { username: status.account.acct || status.account.username, statusId: status.id },
  }).catch(() => {})
}

onMounted(measure)
</script>

<template>
  <div ref="contentEl" class="post-content">
    <div v-if="hasCw" class="post-content__cw">
      <span class="post-content__cw-text" dir="auto"><EmojiText :text="status.spoiler_text" :emojis="status.emojis" /></span>
      <span v-if="!cwOpen && hiddenSummary" class="post-content__cw-meta">{{ hiddenSummary }}</span>
      <button
        type="button"
        class="post-content__cw-toggle"
        :aria-expanded="cwOpen"
        :aria-controls="contentId"
        @click.stop="cwOpen = !cwOpen"
      >
        {{ cwOpen ? '收起' : '显示内容' }}
      </button>
    </div>
    <div v-show="bodyVisible" :id="contentId">
      <div ref="textEl" class="post-content__text" :class="{ 'is-clamped': long && !expanded }">
        <RichText :html="status.content" :emojis="status.emojis" :class="textClass" :dir="textDir(status.language)" />
      </div>
      <button v-if="long" type="button" class="post-content__more" @click.stop="expanded = !expanded">
        {{ expanded ? '收起' : '展开全文' }}
      </button>
      <div v-if="status.media_attachments?.length" class="post-content__media">
        <PostMedia :attachments="status.media_attachments" :status-id="status.id" :hidden="mediaHidden" />
        <button
          v-if="mediaHidden"
          type="button"
          class="post-content__sensitive"
          @click.stop="mediaRevealed = true"
        >
          敏感内容，点击查看
        </button>
        <button
          v-else-if="gated"
          type="button"
          class="post-content__rehide"
          @click.stop="mediaRevealed = false"
        >
          再次隐藏
        </button>
      </div>
      <article v-if="quoted" class="post-quote" role="link" tabindex="0" @click.stop="openQuote" @keydown.enter.stop="openQuote">
        <header class="post-quote__who">
          <EmojiText :text="quoted.account.display_name || quoted.account.username" :emojis="quoted.account.emojis" />
          <span>@{{ displayAccount(quoted.account.acct) }}</span>
        </header>
        <p class="post-quote__text" dir="auto">{{ quoteText || '（这条引用没有文字）' }}</p>
      </article>
      <LinkPreviewCard v-else-if="previewCard && bodyVisible" :card="previewCard" :hide-image="mediaHidden" />
      <PostPoll v-if="poll" :poll="poll" @updated="pollOverride = $event" />
    </div>
  </div>
</template>

<style scoped>
.post-content__cw {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin: var(--space-1) 0;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-sm);
  background: var(--color-fill);
}

.post-content__cw-text {
  flex: 1;
  min-width: 0;
  font-weight: var(--weight-medium);
  color: var(--color-label);
  letter-spacing: 0;
  unicode-bidi: plaintext;
}

.post-content__cw-toggle {
  min-height: 1.75rem;
  padding: 0 var(--space-3);
  border-radius: var(--radius-full);
  background: var(--color-bg-row);
  color: var(--color-accent-text);
  font-size: var(--text-footnote);
  font-weight: var(--weight-semibold);
  box-shadow: var(--shadow-1);
  transition: transform var(--duration-release) var(--ease-out);
}

.post-content__cw-toggle:active {
  transform: scale(var(--press-scale));
  transition-duration: var(--duration-press);
}

.post-content__cw-meta,
.post-content__more {
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
}

.post-content__more {
  min-height: 2.75rem;
  margin-top: var(--space-1);
  color: var(--color-accent-text);
  font-weight: var(--weight-semibold);
}

.post-content__text.is-clamped {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 8;
  overflow: hidden;
}

.post-quote {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin-top: var(--space-2);
  padding: var(--space-3);
  border-radius: var(--radius-md);
  background: var(--color-fill);
  color: inherit;
  min-width: 0;
}

.post-quote__who {
  display: flex;
  gap: var(--space-2);
  min-width: 0;
  font-size: var(--text-footnote);
  font-weight: var(--weight-semibold);
}

.post-quote__who span {
  color: var(--color-label-secondary);
  font-weight: var(--weight-regular);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.post-quote__text {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 4;
  overflow: hidden;
  margin: var(--space-1) 0 0;
  color: var(--color-label);
  font-size: var(--text-subhead);
  line-height: var(--leading-subhead);
  overflow-wrap: break-word;
  letter-spacing: 0;
  unicode-bidi: plaintext;
}

.post-content__media {
  position: relative;
}

/* Hidden media is a BlurHash placeholder (see PostMedia); the button covers it. */
.post-content__rehide {
  position: absolute;
  top: var(--space-2);
  right: var(--space-2);
  padding: var(--space-1) var(--space-3);
  border-radius: var(--radius-sm);
  background: rgb(0 0 0 / 55%);
  color: #fff;
  font-size: var(--text-footnote);
}

.post-content__sensitive {
  position: absolute;
  inset: 0;
  width: 100%;
  min-height: 6rem;
  padding: var(--space-4);
  border-radius: var(--radius-lg);
  background: rgb(0 0 0 / 30%);
  color: #fff;
  font-size: var(--text-subhead);
  font-weight: var(--weight-medium);
  transition: background-color var(--duration-release) var(--ease-out);
}

.post-content__sensitive:active {
  background: rgb(0 0 0 / 40%);
  transition-duration: 0s;
}

@media (max-width: 43.74rem) {
  .post-content__cw-toggle,
  .post-content__sensitive,
  .post-content__rehide,
  .post-content__more {
    min-height: 2.75rem;
  }

  .post-content__text.is-clamped {
    -webkit-line-clamp: 6;
  }

  .post-quote {
    padding: var(--space-2);
  }

  .post-quote__text {
    -webkit-line-clamp: 3;
  }
}
</style>
