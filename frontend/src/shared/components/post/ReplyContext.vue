<script setup lang="ts">
import { computed } from 'vue'
import type { MastodonReplyPreview } from '@/shared/types/mastodon'
import { deletedStatusIds, editedStatuses } from '@/domains/post/presentation/composables/useStatusActions'
import { stripHtml } from '@/shared/utils/sanitize'
import { displayAccount } from '@/shared/utils/accountDisplay'
import { t } from '@/shared/i18n'
import AccountLink from '@/shared/components/common/AccountLink.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import RelativeTime from '@/shared/components/common/RelativeTime.vue'
const props = defineProps<{ status: MastodonReplyPreview }>()
const parent = computed(() => ({ ...props.status, ...editedStatuses.get(props.status.id) }))
const summary = computed(() => {
  if (parent.value.filtered?.length) return t('shared.ReplyContext.filtered')
  if (parent.value.spoiler_text) return parent.value.spoiler_text
  if (parent.value.sensitive) return t('shared.ReplyContext.sensitive')
  return stripHtml(parent.value.content).replace(/\s+/g, ' ').trim() || t('shared.ReplyContext.open')
})
</script>

<template>
  <div v-if="!deletedStatusIds.has(parent.id)" class="reply-context">
    <AccountLink :account="parent.account" class="reply-context__avatar" :aria-label="parent.account.display_name || parent.account.username"><UserAvatar :account="parent.account" :size="32" /></AccountLink>
    <div class="reply-context__body">
      <div class="reply-context__header">
        <AccountLink :account="parent.account" class="reply-context__name"><EmojiText :text="parent.account.display_name || parent.account.username" :emojis="parent.account.emojis" /></AccountLink>
        <span class="reply-context__acct">@{{ displayAccount(parent.account.acct) }}</span>
        <RelativeTime :datetime="parent.created_at" />
      </div>
      <router-link class="reply-context__text" :to="{ name: 'post-detail', params: { username: parent.account.acct, statusId: parent.id } }" @click.stop>
        <span v-if="parent.spoiler_text || parent.sensitive" class="reply-context__cw">CW</span>
        <EmojiText :text="summary" :emojis="parent.emojis" />
      </router-link>
    </div>
  </div>
</template>

<style scoped>
.reply-context { position: relative; display: flex; gap: var(--space-3); padding: 0 0 18px 6px; margin-top: 2px; color: var(--color-label-secondary); font-size: var(--text-subhead); }
.reply-context::after { content: ''; position: absolute; left: 21px; top: 39px; bottom: 6px; width: 2px; border-radius: 2px; background: var(--color-separator); }
.reply-context__avatar { align-self: flex-start; flex: none; }
.reply-context__body { flex: 1; min-width: 0; padding-left: 6px; }
.reply-context__header { display: flex; gap: 6px; align-items: baseline; min-width: 0; }
.reply-context__name { flex: 0 1 auto; font-weight: var(--weight-semibold); overflow: hidden; white-space: nowrap; text-overflow: ellipsis; max-width: 65%; }
.reply-context__acct { flex: 0 2 auto; min-width: 0; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.reply-context__header time { margin-left: auto; flex: none; font-size: var(--text-caption); }
.reply-context__text { display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; color: inherit; text-decoration: none; line-height: 1.5; margin-top: 2px; }
.reply-context__text:hover { color: var(--color-label); }
.reply-context__cw { font-size: var(--text-caption); border: 1px solid var(--color-separator); border-radius: 4px; padding: 0 4px; margin-right: 6px; }
@media (max-width: 480px) { .reply-context__acct { display: none; } }
</style>
