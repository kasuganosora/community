<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { renderEmojiHtml } from '@/domains/emoji/domain/emoji'
import type { MastodonEmoji } from '@/shared/types/mastodon'
import { internalRouteForLink, shortenBareLinks } from '@/shared/utils/links'
import { markBidiNumbersHtml } from '@/shared/utils/bidi'
import { showAccountHover, leaveAccountHover, closeAccountHover, focusAccountHover, useAccountHoverOwner } from '@/shared/composables/useAccountHover'
import { localHosts } from '@/shared/utils/env'
import { nyaizeHtml } from '@/shared/utils/nyaize'

const props = withDefaults(defineProps<{
  html: string
  emojis?: MastodonEmoji[]
  dir?: 'auto' | 'rtl' | 'ltr'
  accountHoverEnabled?: boolean
  /** Render the text nyaized (speakAsCat); display only. */
  nyaize?: boolean
}>(), {
  dir: 'auto',
  accountHoverEnabled: true,
})

const root = ref<HTMLElement>()
useAccountHoverOwner(root)
function onAccountOver(event: Event) {
  if (!props.accountHoverEnabled || ('pointerType' in event && event.pointerType !== 'mouse')) return
  const anchor = (event.target as Element)?.closest?.('a.mention:not(.hashtag)') as HTMLAnchorElement | null
  if (!anchor) return
  const to = internalRouteForLink(anchor.getAttribute('href') || '', anchor.className, localHosts())
  if (to?.startsWith('/@')) showAccountHover(anchor, { acct: to.slice(2) })
}
function onAccountOut(event: MouseEvent | FocusEvent) {
  const anchor = (event.target as Element)?.closest?.('a.mention')
  if (anchor && !anchor.contains(event.relatedTarget as Node)) leaveAccountHover()
}
const revealed = ref(false)
watch(() => props.html, () => { revealed.value = false })
const sensitive = computed(() => props.emojis?.some(e => e.is_sensitive && props.html.includes(`:${e.shortcode}:`)))
const safeHtml = computed(() => markBidiNumbersHtml(shortenBareLinks(renderEmojiHtml(props.nyaize ? nyaizeHtml(props.html) : props.html, props.emojis, revealed.value))))
const router = useRouter()

// Mentions and hashtags open the in-app profile / tag page instead of a new
// tab on the server URL; other links keep opening externally.
function onClick(e: MouseEvent) {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
  const a = (e.target as Element | null)?.closest?.('a')
  if (!a) return
  const to = internalRouteForLink(a.getAttribute('href') ?? '', a.className, localHosts())
  if (!to) return
  e.preventDefault()
  e.stopPropagation()
  closeAccountHover()
  router.push(to).catch(() => {})
}
</script>

<template>
  <div ref="root" class="rich-text" :dir="dir"><div v-html="safeHtml" @click="onClick" @pointerover="onAccountOver" @pointerout="onAccountOut" @focusin="onAccountOver" @focusout="onAccountOut" @keydown="focusAccountHover" /><button v-if="sensitive" type="button" class="emoji-reveal" @click.stop="revealed = !revealed">{{ revealed ? $t('shared.RichText.s1') : $t('shared.RichText.s2') }}</button></div>
</template>

<style scoped>
.rich-text :deep(.custom-emoji) { display: inline-block; width: 1.3em; height: 1.3em; max-width: none; object-fit: contain; vertical-align: -0.25em; }
.emoji-reveal { color: var(--color-accent-text); font-size: var(--text-footnote); }
.rich-text {
  /* Long unbroken words and URLs wrap instead of widening the column. */
  overflow-wrap: anywhere;
  line-height: inherit;
  letter-spacing: 0;
}

.rich-text[dir="auto"],
.rich-text[dir="auto"] :deep(p) {
  unicode-bidi: plaintext;
}

.rich-text[dir="rtl"],
.rich-text[dir="rtl"] :deep(p) {
  letter-spacing: 0;
}

/* Keep @mentions, hashtags and URLs in their own direction so a following
   number or Latin word is not pulled into the Arabic run. */
.rich-text :deep(a),
.rich-text :deep(code) {
  unicode-bidi: isolate;
  direction: ltr;
}

.rich-text :deep(p) {
  margin: 0 0 0.5em;
}

.rich-text :deep(p:last-child) {
  margin-bottom: 0;
}

/* Links, mentions and hashtags share one quiet tint: the accent is the only
   colour in body text, and weight stays regular so it doesn't shout. */
.rich-text :deep(a) {
  color: var(--color-accent-text);
  text-decoration: none;
  border-radius: 3px;
  -webkit-tap-highlight-color: transparent;
}

.rich-text :deep(a:active) {
  background: var(--color-accent-soft);
}

@media (hover: hover) and (pointer: fine) {
  .rich-text :deep(a:hover) {
    text-decoration: underline;
    text-decoration-thickness: 1px;
    text-underline-offset: 2px;
  }
}

/* Mastodon marks the hidden parts of long remote URLs with these classes. */
.rich-text :deep(.invisible) {
  display: none;
}

.rich-text :deep(.ellipsis)::after {
  content: '…';
}

/* Long bare URLs wrap instead of pushing the column wider. */
.rich-text :deep(a:not(.mention)) {
  word-break: break-all;
}

.rich-text :deep(code) {
  font-size: 0.875em;
  padding: 0.1em 0.35em;
  border-radius: var(--radius-xs);
  background: var(--color-fill);
}
</style>
