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
    <blockquote v-if="message.reply_event_id && !message.redacted" class="chat-message__reply">{{ reply?.redacted ? '消息已删除' : reply ? reply.body : '回复一条消息' }}</blockquote>
    <p v-if="message.redacted" class="chat-message__deleted">消息已删除</p>
    <PostContent v-else-if="message.status" :status="message.status" />
    <template v-else>
      <p class="chat-message__body" dir="auto">{{ message.body }}</p>
      <template v-if="mediaUrl">
        <img v-if="message.msgtype === 'm.image'" class="chat-message__image" :src="mediaUrl" :alt="message.body || '消息图片'" loading="lazy" />
        <video v-else-if="message.msgtype === 'm.video'" class="chat-message__image" :src="mediaUrl" controls preload="metadata" />
        <audio v-else-if="message.msgtype === 'm.audio'" :src="mediaUrl" controls preload="metadata" />
        <a :href="mediaUrl" target="_blank" rel="noopener noreferrer">打开附件</a>
      </template>
    </template>
    <div v-if="!message.redacted" class="chat-message__footer">
      <span v-if="message.own && message.delivery" class="chat-message__delivery" :class="{ 'chat-message__delivery--failed': message.delivery.state === 'failed' }" role="status">{{ message.delivery.state === 'delivered' && !message.delivery.remote_received ? '已投递（本地）' : deliveryLabel[message.delivery.state] }}</span>
      <button v-if="message.own && message.delivery && ['queued', 'sending'].includes(message.delivery.state)" class="btn btn--ghost btn--sm" :disabled="pending" @click="emit('refreshDelivery')">刷新状态</button>
      <span v-if="message.own && message.delivery?.error_code" class="chat-message__delivery">{{ message.delivery.error_code === 'federation_delivery_failed' ? 'ActivityPub 服务器投递失败' : message.delivery.error_code === 'matrix_delivery_failed' ? 'Matrix 服务器投递失败' : '消息处理失败' }}</span>
      <button v-if="message.own && message.delivery?.can_retry" class="btn btn--ghost btn--sm" :disabled="pending" @click="emit('retry')">重试投递</button>
      <button class="btn btn--ghost btn--sm" :disabled="pending" @click="emit('reply')">回复</button>
      <button v-if="message.own" class="btn btn--ghost btn--sm" :disabled="pending || !message.status_id" :title="!message.status_id ? '消息正在处理，完成后可以编辑' : undefined" @click="emit('edit')">{{ message.status_id ? '编辑' : '处理后可编辑' }}</button>
      <button v-if="message.own" class="btn btn--ghost btn--sm" :disabled="pending" @click="emit('remove')">删除</button>
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
.chat-message__image { display: block; max-width: 100%; max-height: 400px; border-radius: var(--radius-sm); }
.chat-message audio { max-width: 100%; }
.chat-message__footer { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-1); margin-top: var(--space-2); }
.chat-message__delivery { margin-right: auto; font-size: var(--text-caption); color: var(--color-label-secondary); }
.chat-message__delivery--failed { color: var(--color-danger); }
</style>
