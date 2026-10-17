<script setup lang="ts">
import { computed } from 'vue'
import type { ChatMessage } from '../../domain/chat'
import { deliveryLabel } from '../../domain/chat'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import PostContent from '@/shared/components/post/PostContent.vue'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import AccountLink from '@/shared/components/common/AccountLink.vue'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { stripHtml } from '@/shared/utils/sanitize'
import { messagePreview } from '../../domain/messagePreview'
import { t } from '@/shared/i18n'

const props = defineProps<{ message: ChatMessage; pending?: boolean; reply?: ChatMessage; account?: MastodonAccount }>()
const emit = defineEmits<{ reply: []; edit: []; remove: []; retry: []; refreshDelivery: [] }>()
const mediaUrl = computed(() => {
  if (!props.message.url) return ''
  try {
    const url = new URL(props.message.url, window.location.origin)
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : ''
  } catch { return '' }
})
const author = computed(() => props.message.status?.account || props.account)
const createdAt = computed(() => new Date(props.message.created_at))
const replyText = computed(() => {
  const reply = props.reply
  if (reply?.redacted) return t('message.ChatMessageView.s1')
  if (!reply) return t('message.ChatMessageView.s2')
  if (reply.status?.spoiler_text) return reply.status.spoiler_text
  if (reply.status?.sensitive) return t('message.chat.attachment')
  return messagePreview(reply.status ? stripHtml(reply.status.content) : reply.body)
})
</script>

<template>
  <article class="chat-message" :class="{ 'chat-message--own': message.own }" :data-event-id="message.event_id">
    <AccountLink v-if="author" :account="author" class="chat-message__avatar" :aria-label="author.display_name || author.username"><UserAvatar :account="author" :size="32" /></AccountLink>
    <span v-else class="chat-message__avatar chat-message__avatar--fallback" aria-hidden="true"><t-icon name="user" :size="20" /></span>
    <div class="chat-message__content">
      <div v-if="!message.own" class="chat-message__header">
        <AccountLink v-if="author" :account="author"><EmojiText :text="author.display_name || author.username" :emojis="author.emojis" /></AccountLink>
        <span v-else>{{ message.sender }}</span>
      </div>
      <div class="chat-message__bubble">
    <blockquote v-if="message.reply_event_id && !message.redacted" class="chat-message__reply"><t-icon name="rollback" :size="14" /><span>{{ replyText }}</span></blockquote>
    <p v-if="message.redacted" class="chat-message__deleted">{{ $t('message.ChatMessageView.s1') }}</p>
    <PostContent v-else-if="message.status" :status="message.status" />
    <template v-else>
      <p class="chat-message__body" dir="auto">{{ message.body }}</p>
      <template v-if="mediaUrl">
        <img v-if="message.msgtype === 'm.image'" class="chat-message__image" :src="mediaUrl" :alt="message.body || $t('message.ChatMessageView.s3')" loading="lazy" />
        <video v-else-if="message.msgtype === 'm.video'" class="chat-message__image" :src="mediaUrl" controls preload="metadata" />
        <audio v-else-if="message.msgtype === 'm.audio'" :src="mediaUrl" controls preload="metadata" />
        <a :href="mediaUrl" target="_blank" rel="noopener noreferrer">{{ $t('message.ChatMessageView.s4') }}</a>
      </template>
    </template>
      </div>
      <div class="chat-message__meta">
        <time :datetime="createdAt.toISOString()" :title="createdAt.toLocaleString()">{{ createdAt.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) }}</time>
    <div v-if="!message.redacted" class="chat-message__footer">
      <span v-if="message.own && message.delivery" class="chat-message__delivery" :class="{ 'chat-message__delivery--failed': message.delivery.state === 'failed' }" role="status">{{ message.delivery.state === 'delivered' && !message.delivery.remote_received ? $t('message.ChatMessageView.s5') : deliveryLabel[message.delivery.state] }}</span>
      <button v-if="message.own && message.delivery && ['queued', 'sending'].includes(message.delivery.state)" class="btn btn--ghost btn--sm" :disabled="pending" @click="emit('refreshDelivery')">{{ $t('message.ChatMessageView.s6') }}</button>
      <span v-if="message.own && message.delivery?.error_code" class="chat-message__delivery">{{ message.delivery.error_code === 'federation_delivery_failed' ? $t('message.ChatMessageView.s7') : message.delivery.error_code === 'matrix_delivery_failed' ? $t('message.ChatMessageView.s8') : $t('message.ChatMessageView.s9') }}</span>
      <button v-if="message.own && message.delivery?.can_retry" class="btn btn--ghost btn--sm" :disabled="pending" @click="emit('retry')">{{ $t('message.ChatMessageView.s10') }}</button>
      <button class="btn btn--ghost btn--sm" :disabled="pending" @click="emit('reply')">{{ $t('message.ChatMessageView.s11') }}</button>
      <button v-if="message.own" class="btn btn--ghost btn--sm" :disabled="pending || !message.status_id" :title="!message.status_id ? $t('message.ChatMessageView.s12') : undefined" @click="emit('edit')">{{ message.status_id ? $t('message.ChatMessageView.s13') : $t('message.ChatMessageView.s14') }}</button>
      <button v-if="message.own" class="btn btn--ghost btn--sm" :disabled="pending" @click="emit('remove')">{{ $t('message.ChatMessageView.s15') }}</button>
    </div>
      </div>
    </div>
  </article>
</template>

<style scoped>
.chat-message { display: flex; align-items: flex-start; gap: var(--space-2); margin-block: var(--space-4); min-width: 0; overflow-wrap: anywhere; }
.chat-message--own { flex-direction: row-reverse; }
.chat-message__avatar { flex: 0 0 32px; margin-top: var(--space-1); }
.chat-message__avatar--fallback { display: grid; place-items: center; width: 32px; height: 32px; border-radius: 50%; background: var(--color-fill); color: var(--color-label-secondary); }
.chat-message__content { min-width: 0; max-width: min(80%, 38rem); }
.chat-message__header { margin: 0 var(--space-2) var(--space-1); color: var(--color-label-secondary); font-size: var(--text-footnote); }
.chat-message__bubble { padding: var(--space-3) var(--space-4); border-radius: 4px 18px 18px; background: var(--color-bg-grouped); border: 1px solid var(--color-separator); }
.chat-message--own .chat-message__bubble { border-radius: 18px 4px 18px 18px; background: var(--color-accent-soft); border-color: transparent; }
.chat-message__body { white-space: pre-wrap; margin: 0; line-height: 1.65; }
.chat-message__deleted { color: var(--color-label-secondary); margin: 0; font-size: var(--text-footnote); }
.chat-message__reply { display: flex; align-items: baseline; gap: var(--space-1); margin: 0 0 var(--space-2); padding: var(--space-2); border-left: 2px solid var(--color-accent); background: var(--color-fill); border-radius: 0 var(--radius-sm) var(--radius-sm) 0; color: var(--color-label-secondary); font-size: var(--text-footnote); }
.chat-message__reply > span { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; }
.chat-message__reply :deep(svg) { flex-shrink: 0; }
.chat-message__image { display: block; width: min(100%, 28rem); max-height: 400px; object-fit: contain; background: var(--color-fill); border-radius: var(--radius-sm); margin-block: var(--space-2); }
.chat-message audio { max-width: 100%; }
.chat-message__meta { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; padding-inline: var(--space-2); color: var(--color-label-secondary); font-size: var(--text-caption); }
.chat-message--own .chat-message__meta { justify-content: flex-end; }
.chat-message__meta time { font-variant-numeric: tabular-nums; flex-shrink: 0; }
.chat-message__footer { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-1); }
.chat-message__footer .btn { color: var(--color-label-secondary); font-size: var(--text-caption); padding-inline: var(--space-2); min-height: 32px; }
.chat-message__footer .btn:hover { color: var(--color-accent-text); }
.chat-message__delivery { font-size: var(--text-caption); color: var(--color-label-secondary); }
.chat-message__delivery--failed { color: var(--color-danger); }
@media (max-width: 43.74rem) {
  .chat-message__content { max-width: calc(100% - 52px); }
  .chat-message__bubble { padding: var(--space-2) var(--space-3); }
  .chat-message__footer .btn { min-height: var(--hit-target); }
}
</style>
