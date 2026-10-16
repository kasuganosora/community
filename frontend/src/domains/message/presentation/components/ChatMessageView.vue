<script setup lang="ts">
import { computed } from 'vue'
import type { ChatMessage } from '../../domain/chat'
import { deliveryLabel } from '../../domain/chat'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import PostContent from '@/shared/components/post/PostContent.vue'
import RelativeTime from '@/shared/components/common/RelativeTime.vue'

const props = defineProps<{ message: ChatMessage; pending?: boolean; reply?: ChatMessage }>()
const emit = defineEmits<{ reply: []; edit: []; remove: []; retry: []; refreshDelivery: [] }>()
const mediaUrl = computed(() => {
  if (!props.message.url) return ''
  try {
    const url = new URL(props.message.url, window.location.origin)
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : ''
  } catch { return '' }
})
const createdAt = computed(() => new Date(props.message.created_at).toISOString())
</script>

<template>
  <article class="chat-message" :class="{ 'chat-message--own': message.own }" :data-event-id="message.event_id">
    <div class="chat-message__header">
      <UserAvatar v-if="message.status" :account="message.status.account" :size="28" />
      <span class="chat-message__sender">{{ message.status?.account.display_name || message.sender }}</span>
      <RelativeTime :datetime="createdAt" />
    </div>
    <blockquote v-if="message.reply_event_id && !message.redacted" class="chat-message__reply">{{ reply?.redacted ? $t('message.ChatMessageView.s1') : reply ? reply.body : $t('message.ChatMessageView.s2') }}</blockquote>
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
    <div v-if="!message.redacted" class="chat-message__footer">
      <span v-if="message.own && message.delivery" class="chat-message__delivery" :class="{ 'chat-message__delivery--failed': message.delivery.state === 'failed' }" role="status">{{ message.delivery.state === 'delivered' && !message.delivery.remote_received ? $t('message.ChatMessageView.s5') : deliveryLabel[message.delivery.state] }}</span>
      <button v-if="message.own && message.delivery && ['queued', 'sending'].includes(message.delivery.state)" class="btn btn--ghost btn--sm" :disabled="pending" @click="emit('refreshDelivery')">{{ $t('message.ChatMessageView.s6') }}</button>
      <span v-if="message.own && message.delivery?.error_code" class="chat-message__delivery">{{ message.delivery.error_code === 'federation_delivery_failed' ? $t('message.ChatMessageView.s7') : message.delivery.error_code === 'matrix_delivery_failed' ? $t('message.ChatMessageView.s8') : $t('message.ChatMessageView.s9') }}</span>
      <button v-if="message.own && message.delivery?.can_retry" class="btn btn--ghost btn--sm" :disabled="pending" @click="emit('retry')">{{ $t('message.ChatMessageView.s10') }}</button>
      <button class="btn btn--ghost btn--sm" :disabled="pending" @click="emit('reply')">{{ $t('message.ChatMessageView.s11') }}</button>
      <button v-if="message.own" class="btn btn--ghost btn--sm" :disabled="pending || !message.status_id" :title="!message.status_id ? $t('message.ChatMessageView.s12') : undefined" @click="emit('edit')">{{ message.status_id ? $t('message.ChatMessageView.s13') : $t('message.ChatMessageView.s14') }}</button>
      <button v-if="message.own" class="btn btn--ghost btn--sm" :disabled="pending" @click="emit('remove')">{{ $t('message.ChatMessageView.s15') }}</button>
    </div>
  </article>
</template>

<style scoped>
.chat-message { margin: var(--space-3) var(--gutter, var(--space-4)); padding: var(--space-3); min-width: 0; border: 1px solid var(--color-separator); border-radius: var(--radius-lg, 14px); overflow-wrap: anywhere; }
.chat-message--own { background: var(--color-fill); }
.chat-message__header { display: flex; align-items: center; gap: var(--space-2); color: var(--color-label-secondary); font-size: var(--text-footnote); }
.chat-message__sender { flex: 1; min-width: 0; }
.chat-message__body { white-space: pre-wrap; margin-block: var(--space-3); }
.chat-message__reply { margin: var(--space-2) 0; padding-left: var(--space-2); border-left: 2px solid var(--color-separator); color: var(--color-label-secondary); font-size: var(--text-footnote); max-height: 3em; overflow: hidden; }
.chat-message__image { display: block; width: min(100%, 28rem); max-height: 400px; aspect-ratio: 4 / 3; object-fit: contain; background: var(--color-fill); border-radius: var(--radius-sm); }
.chat-message audio { max-width: 100%; }
.chat-message__footer { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-1); margin-top: var(--space-2); }
.chat-message__delivery { margin-right: auto; font-size: var(--text-caption); color: var(--color-label-secondary); }
.chat-message__delivery--failed { color: var(--color-danger); }
</style>
